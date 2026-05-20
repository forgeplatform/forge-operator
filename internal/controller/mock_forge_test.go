package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/forgeplatform/forge-operator/internal/forgeapi"
)

// mockForge is a tiny stateful HTTP server that mimics the subset of the
// Forge REST API the operator uses. Each resource type is a map keyed by
// numeric ID. Tests assert against this state to verify reconcile.
type mockForge struct {
	mu sync.Mutex

	nextID int64

	organizations   map[int64]map[string]any
	credentialTypes map[int64]map[string]any
	projects        map[int64]map[string]any
	teams           map[int64]map[string]any
	users           map[int64]map[string]any
	teamUsers       map[int64]map[int64]struct{} // team ID -> set of user IDs

	inventories  map[int64]map[string]any
	hosts        map[int64]map[string]any
	groups       map[int64]map[string]any
	groupHosts   map[int64]map[int64]struct{} // group ID -> set of host IDs
	groupChild   map[int64]map[int64]struct{} // group ID -> set of child group IDs
	credentials  map[int64]map[string]any
	jobTemplates map[int64]map[string]any
	jtCreds      map[int64]map[int64]struct{} // jt ID -> set of credential IDs
	schedules    map[int64]map[string]any

	workflows     map[int64]map[string]any
	workflowNodes map[int64]map[string]any           // node ID -> node
	nodeEdges     map[int64]map[string]map[int64]struct{} // src -> edge -> set of target IDs

	// Counters for assertions.
	calls map[string]int
}

func newMockForge() *mockForge {
	m := &mockForge{
		nextID:          100,
		organizations:   map[int64]map[string]any{},
		credentialTypes: map[int64]map[string]any{},
		projects:        map[int64]map[string]any{},
		teams:           map[int64]map[string]any{},
		users:           map[int64]map[string]any{},
		teamUsers:       map[int64]map[int64]struct{}{},
		inventories:     map[int64]map[string]any{},
		hosts:           map[int64]map[string]any{},
		groups:          map[int64]map[string]any{},
		groupHosts:      map[int64]map[int64]struct{}{},
		groupChild:      map[int64]map[int64]struct{}{},
		credentials:     map[int64]map[string]any{},
		jobTemplates:    map[int64]map[string]any{},
		jtCreds:         map[int64]map[int64]struct{}{},
		schedules:       map[int64]map[string]any{},
		workflows:       map[int64]map[string]any{},
		workflowNodes:   map[int64]map[string]any{},
		nodeEdges:       map[int64]map[string]map[int64]struct{}{},
		calls:           map[string]int{},
	}
	// Seed defaults.
	m.organizations[1] = map[string]any{"id": int64(1), "name": "Default"}
	m.credentialTypes[1] = map[string]any{"id": int64(1), "name": "Machine", "kind": "ssh"}
	m.projects[1] = map[string]any{"id": int64(1), "name": "Demo Project", "organization": int64(1)}
	m.users[1] = map[string]any{"id": int64(1), "username": "admin"}
	return m
}

// usersByUsername returns users matching the given username (or all if empty).
func usersByUsername(m map[int64]map[string]any, username string) []map[string]any {
	out := []map[string]any{}
	for _, v := range m {
		if username == "" || v["username"] == username {
			out = append(out, v)
		}
	}
	return out
}

// CallCount returns how many times handlerKey was hit.
func (m *mockForge) CallCount(handlerKey string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[handlerKey]
}

func (m *mockForge) start(t *testing.T) (*httptest.Server, *forgeapi.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(srv.Close)
	return srv, newTestForgeClient(srv.URL, "test-token")
}

func newTestForgeClient(baseURL, token string) *forgeapi.Client {
	return forgeapi.New(baseURL, token, "", true)
}

// newTestClientPool builds a ClientPool wrapping the default client and
// the manager's k8s client (used to look up ForgeInstance CRs in tests
// that exercise the multi-cluster path).
func newTestClientPool(def *forgeapi.Client, k client.Client) *forgeapi.ClientPool {
	return forgeapi.NewClientPool(def, k)
}

// listEnvelope produces the Forge `{count, results}` shape.
func listEnvelope(items []map[string]any) []byte {
	out := map[string]any{
		"count":   len(items),
		"results": items,
	}
	b, _ := json.Marshal(out)
	return b
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// findByName scans a resource map for an exact-name match and returns
// the items in {count,results} format with optional name filter.
func findByName(m map[int64]map[string]any, name string) []map[string]any {
	out := []map[string]any{}
	for _, v := range m {
		if name == "" || v["name"] == name {
			out = append(out, v)
		}
	}
	return out
}

// idFromPath extracts a numeric ID from /api/v2/<resource>/<id>/...
func idFromPath(path, prefix string) (int64, string) {
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return -1, ""
	}
	tail := ""
	if len(parts) == 2 {
		tail = "/" + parts[1]
	}
	return id, tail
}

func (m *mockForge) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Bearer auth check
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "no auth", http.StatusUnauthorized)
		return
	}

	q := r.URL.Query().Get("name")
	path := r.URL.Path

	switch {
	// --- ping (health probe used by ForgeInstance) ---
	case r.Method == "GET" && path == "/api/v2/ping/":
		m.calls["GET ping"]++
		writeJSON(w, http.StatusOK, map[string]any{"version": "2026.04.0", "install_uuid": "test"})

	// --- name -> id resolvers (lists with name= filter) ---
	case r.Method == "GET" && path == "/api/v2/organizations/":
		m.calls["GET organizations"]++
		w.Write(listEnvelope(findByName(m.organizations, q)))
	case r.Method == "POST" && path == "/api/v2/organizations/":
		m.calls["POST organizations"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		m.organizations[id] = b
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/organizations/"):
		id, _ := idFromPath(path, "/api/v2/organizations/")
		switch r.Method {
		case "GET":
			m.calls["GET organization"]++
			if v, ok := m.organizations[id]; ok {
				writeJSON(w, http.StatusOK, v)
			} else {
				http.NotFound(w, r)
			}
		case "PATCH":
			m.calls["PATCH organization"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cur := m.organizations[id]
			for k, v := range b {
				cur[k] = v
			}
			writeJSON(w, http.StatusOK, cur)
		case "DELETE":
			m.calls["DELETE organization"]++
			delete(m.organizations, id)
			w.WriteHeader(http.StatusNoContent)
		}
	case r.Method == "GET" && path == "/api/v2/credential_types/":
		m.calls["GET credential_types"]++
		w.Write(listEnvelope(findByName(m.credentialTypes, q)))
	case r.Method == "GET" && path == "/api/v2/projects/":
		m.calls["GET projects"]++
		w.Write(listEnvelope(findByName(m.projects, q)))
	case r.Method == "POST" && path == "/api/v2/projects/":
		m.calls["POST projects"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		m.projects[id] = b
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/projects/"):
		id, _ := idFromPath(path, "/api/v2/projects/")
		switch r.Method {
		case "GET":
			m.calls["GET project"]++
			if v, ok := m.projects[id]; ok {
				writeJSON(w, http.StatusOK, v)
			} else {
				http.NotFound(w, r)
			}
		case "PATCH":
			m.calls["PATCH project"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cur := m.projects[id]
			for k, v := range b {
				cur[k] = v
			}
			writeJSON(w, http.StatusOK, cur)
		case "DELETE":
			m.calls["DELETE project"]++
			delete(m.projects, id)
			w.WriteHeader(http.StatusNoContent)
		}

	// --- inventories ---
	case r.Method == "GET" && path == "/api/v2/inventories/":
		m.calls["GET inventories"]++
		w.Write(listEnvelope(findByName(m.inventories, q)))
	case r.Method == "POST" && path == "/api/v2/inventories/":
		m.calls["POST inventories"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		b["total_hosts"] = int32(0)
		b["total_groups"] = int32(0)
		m.inventories[id] = b
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/inventories/"):
		m.handleInventoryNested(w, r, path)
		return

	// --- hosts (top-level for PATCH/DELETE) ---
	case strings.HasPrefix(path, "/api/v2/hosts/"):
		id, _ := idFromPath(path, "/api/v2/hosts/")
		switch r.Method {
		case "PATCH":
			m.calls["PATCH hosts"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cur := m.hosts[id]
			for k, v := range b {
				cur[k] = v
			}
			writeJSON(w, http.StatusOK, cur)
		case "DELETE":
			m.calls["DELETE hosts"]++
			delete(m.hosts, id)
			w.WriteHeader(http.StatusNoContent)
		}

	// --- groups (top-level + memberships) ---
	case strings.HasPrefix(path, "/api/v2/groups/"):
		m.handleGroupNested(w, r, path)
		return

	// --- credentials ---
	case r.Method == "GET" && path == "/api/v2/credentials/":
		m.calls["GET credentials"]++
		w.Write(listEnvelope(findByName(m.credentials, q)))
	case r.Method == "POST" && path == "/api/v2/credentials/":
		m.calls["POST credentials"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		m.credentials[id] = b
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/credentials/"):
		id, _ := idFromPath(path, "/api/v2/credentials/")
		switch r.Method {
		case "GET":
			m.calls["GET credential"]++
			if v, ok := m.credentials[id]; ok {
				writeJSON(w, http.StatusOK, v)
			} else {
				http.NotFound(w, r)
			}
		case "PATCH":
			m.calls["PATCH credential"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cur := m.credentials[id]
			for k, v := range b {
				cur[k] = v
			}
			writeJSON(w, http.StatusOK, cur)
		case "DELETE":
			m.calls["DELETE credential"]++
			delete(m.credentials, id)
			w.WriteHeader(http.StatusNoContent)
		}

	// --- job_templates ---
	case r.Method == "GET" && path == "/api/v2/job_templates/":
		m.calls["GET job_templates"]++
		w.Write(listEnvelope(findByName(m.jobTemplates, q)))
	case r.Method == "POST" && path == "/api/v2/job_templates/":
		m.calls["POST job_templates"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		m.jobTemplates[id] = b
		m.jtCreds[id] = map[int64]struct{}{}
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/job_templates/"):
		m.handleJobTemplateNested(w, r, path)
		return

	// --- teams ---
	case r.Method == "GET" && path == "/api/v2/teams/":
		m.calls["GET teams"]++
		w.Write(listEnvelope(findByName(m.teams, q)))
	case r.Method == "POST" && path == "/api/v2/teams/":
		m.calls["POST teams"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		m.teams[id] = b
		m.teamUsers[id] = map[int64]struct{}{}
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/teams/"):
		id, tail := idFromPath(path, "/api/v2/teams/")
		switch {
		case tail == "/" || tail == "":
			switch r.Method {
			case "GET":
				m.calls["GET team"]++
				if v, ok := m.teams[id]; ok {
					writeJSON(w, http.StatusOK, v)
				} else {
					http.NotFound(w, r)
				}
			case "PATCH":
				m.calls["PATCH team"]++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				cur := m.teams[id]
				for k, v := range b {
					cur[k] = v
				}
				writeJSON(w, http.StatusOK, cur)
			case "DELETE":
				m.calls["DELETE team"]++
				delete(m.teams, id)
				delete(m.teamUsers, id)
				w.WriteHeader(http.StatusNoContent)
			}
		case strings.HasPrefix(tail, "/users/"):
			switch r.Method {
			case "GET":
				m.calls["GET team users"]++
				items := []map[string]any{}
				for uid := range m.teamUsers[id] {
					if u, ok := m.users[uid]; ok {
						items = append(items, u)
					}
				}
				w.Write(listEnvelope(items))
			case "POST":
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				uid := int64(b["id"].(float64))
				if d, ok := b["disassociate"]; ok && d.(bool) {
					m.calls["DISASSOCIATE team user"]++
					delete(m.teamUsers[id], uid)
				} else {
					m.calls["ASSOCIATE team user"]++
					m.teamUsers[id][uid] = struct{}{}
				}
				w.WriteHeader(http.StatusNoContent)
			}
		}

	// --- workflows ---
	case r.Method == "GET" && path == "/api/v2/workflow_job_templates/":
		m.calls["GET workflows"]++
		w.Write(listEnvelope(findByName(m.workflows, q)))
	case r.Method == "POST" && path == "/api/v2/workflow_job_templates/":
		m.calls["POST workflows"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		m.workflows[id] = b
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/workflow_job_templates/"):
		id, tail := idFromPath(path, "/api/v2/workflow_job_templates/")
		switch {
		case tail == "/" || tail == "":
			switch r.Method {
			case "GET":
				m.calls["GET workflow"]++
				if v, ok := m.workflows[id]; ok {
					writeJSON(w, http.StatusOK, v)
				} else {
					http.NotFound(w, r)
				}
			case "PATCH":
				m.calls["PATCH workflow"]++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				cur := m.workflows[id]
				for k, v := range b {
					cur[k] = v
				}
				writeJSON(w, http.StatusOK, cur)
			case "DELETE":
				m.calls["DELETE workflow"]++
				delete(m.workflows, id)
				// Cascade-delete nodes belonging to this workflow.
				for nid, n := range m.workflowNodes {
					if int64(n["workflow_job_template"].(float64)) == id {
						delete(m.workflowNodes, nid)
						delete(m.nodeEdges, nid)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}
		case strings.HasPrefix(tail, "/workflow_nodes/"):
			switch r.Method {
			case "GET":
				m.calls["GET workflow nodes"]++
				items := []map[string]any{}
				for _, n := range m.workflowNodes {
					if int64(n["workflow_job_template"].(float64)) == id {
						items = append(items, n)
					}
				}
				w.Write(listEnvelope(items))
			case "POST":
				m.calls["POST workflow node"]++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				nid := m.nextID
				m.nextID++
				b["id"] = nid
				b["workflow_job_template"] = float64(id)
				m.workflowNodes[nid] = b
				m.nodeEdges[nid] = map[string]map[int64]struct{}{
					"success": {}, "failure": {}, "always": {},
				}
				writeJSON(w, http.StatusCreated, b)
			}
		}
	case strings.HasPrefix(path, "/api/v2/workflow_job_template_nodes/"):
		id, tail := idFromPath(path, "/api/v2/workflow_job_template_nodes/")
		switch {
		case tail == "/" || tail == "":
			switch r.Method {
			case "PATCH":
				m.calls["PATCH workflow node"]++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				cur := m.workflowNodes[id]
				for k, v := range b {
					cur[k] = v
				}
				writeJSON(w, http.StatusOK, cur)
			case "DELETE":
				m.calls["DELETE workflow node"]++
				delete(m.workflowNodes, id)
				delete(m.nodeEdges, id)
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			// /success_nodes/, /failure_nodes/, /always_nodes/
			var edge string
			switch {
			case strings.HasPrefix(tail, "/success_nodes/"):
				edge = "success"
			case strings.HasPrefix(tail, "/failure_nodes/"):
				edge = "failure"
			case strings.HasPrefix(tail, "/always_nodes/"):
				edge = "always"
			}
			if edge == "" {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case "GET":
				m.calls["GET workflow edges"]++
				items := []map[string]any{}
				for tid := range m.nodeEdges[id][edge] {
					items = append(items, map[string]any{"id": tid})
				}
				w.Write(listEnvelope(items))
			case "POST":
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				tid := int64(b["id"].(float64))
				if d, ok := b["disassociate"]; ok && d.(bool) {
					m.calls["DISASSOCIATE workflow edge"]++
					delete(m.nodeEdges[id][edge], tid)
				} else {
					m.calls["ASSOCIATE workflow edge"]++
					m.nodeEdges[id][edge][tid] = struct{}{}
				}
				w.WriteHeader(http.StatusNoContent)
			}
		}

	// --- users (username lookup only) ---
	case r.Method == "GET" && path == "/api/v2/users/":
		m.calls["GET users"]++
		username := r.URL.Query().Get("username")
		w.Write(listEnvelope(usersByUsername(m.users, username)))

	// --- schedules ---
	case r.Method == "GET" && path == "/api/v2/schedules/":
		m.calls["GET schedules"]++
		w.Write(listEnvelope(findByName(m.schedules, q)))
	case r.Method == "POST" && path == "/api/v2/schedules/":
		m.calls["POST schedules"]++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		id := m.nextID
		m.nextID++
		b["id"] = id
		b["next_run"] = "2026-04-30T02:00:00Z"
		m.schedules[id] = b
		writeJSON(w, http.StatusCreated, b)
	case strings.HasPrefix(path, "/api/v2/schedules/"):
		id, _ := idFromPath(path, "/api/v2/schedules/")
		switch r.Method {
		case "GET":
			m.calls["GET schedule"]++
			if v, ok := m.schedules[id]; ok {
				writeJSON(w, http.StatusOK, v)
			} else {
				http.NotFound(w, r)
			}
		case "PATCH":
			m.calls["PATCH schedule"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cur := m.schedules[id]
			for k, v := range b {
				cur[k] = v
			}
			writeJSON(w, http.StatusOK, cur)
		case "DELETE":
			m.calls["DELETE schedule"]++
			delete(m.schedules, id)
			w.WriteHeader(http.StatusNoContent)
		}

	default:
		http.Error(w, fmt.Sprintf("unhandled %s %s", r.Method, path), http.StatusNotImplemented)
	}
}

func (m *mockForge) handleInventoryNested(w http.ResponseWriter, r *http.Request, path string) {
	id, tail := idFromPath(path, "/api/v2/inventories/")
	if id < 0 {
		http.NotFound(w, r)
		return
	}
	inv, ok := m.inventories[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case tail == "/" || tail == "":
		switch r.Method {
		case "GET":
			m.calls["GET inventory"]++
			// Recompute totals.
			hosts := 0
			groups := 0
			for _, h := range m.hosts {
				if h["inventory"] == id {
					hosts++
				}
			}
			for _, g := range m.groups {
				if g["inventory"] == id {
					groups++
				}
			}
			inv["total_hosts"] = int32(hosts)
			inv["total_groups"] = int32(groups)
			writeJSON(w, http.StatusOK, inv)
		case "PATCH":
			m.calls["PATCH inventory"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			for k, v := range b {
				inv[k] = v
			}
			writeJSON(w, http.StatusOK, inv)
		case "DELETE":
			m.calls["DELETE inventory"]++
			delete(m.inventories, id)
			w.WriteHeader(http.StatusNoContent)
		}
	case strings.HasPrefix(tail, "/hosts/"):
		switch r.Method {
		case "GET":
			m.calls["GET inventory hosts"]++
			items := []map[string]any{}
			for _, h := range m.hosts {
				if h["inventory"] == id {
					items = append(items, h)
				}
			}
			w.Write(listEnvelope(items))
		case "POST":
			m.calls["POST inventory host"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			hid := m.nextID
			m.nextID++
			b["id"] = hid
			b["inventory"] = id
			m.hosts[hid] = b
			writeJSON(w, http.StatusCreated, b)
		}
	case strings.HasPrefix(tail, "/groups/"):
		switch r.Method {
		case "GET":
			m.calls["GET inventory groups"]++
			items := []map[string]any{}
			for _, g := range m.groups {
				if g["inventory"] == id {
					items = append(items, g)
				}
			}
			w.Write(listEnvelope(items))
		case "POST":
			m.calls["POST inventory group"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			gid := m.nextID
			m.nextID++
			b["id"] = gid
			b["inventory"] = id
			m.groups[gid] = b
			m.groupHosts[gid] = map[int64]struct{}{}
			m.groupChild[gid] = map[int64]struct{}{}
			writeJSON(w, http.StatusCreated, b)
		}
	}
}

func (m *mockForge) handleGroupNested(w http.ResponseWriter, r *http.Request, path string) {
	id, tail := idFromPath(path, "/api/v2/groups/")
	if id < 0 {
		http.NotFound(w, r)
		return
	}
	g, ok := m.groups[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case tail == "/" || tail == "":
		switch r.Method {
		case "PATCH":
			m.calls["PATCH group"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			for k, v := range b {
				g[k] = v
			}
			writeJSON(w, http.StatusOK, g)
		case "DELETE":
			m.calls["DELETE group"]++
			delete(m.groups, id)
			w.WriteHeader(http.StatusNoContent)
		}
	case strings.HasPrefix(tail, "/hosts/"):
		switch r.Method {
		case "GET":
			m.calls["GET group hosts"]++
			items := []map[string]any{}
			for hid := range m.groupHosts[id] {
				if h, ok := m.hosts[hid]; ok {
					items = append(items, h)
				}
			}
			w.Write(listEnvelope(items))
		case "POST":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			hid := int64(b["id"].(float64))
			if d, ok := b["disassociate"]; ok && d.(bool) {
				m.calls["DISASSOCIATE group host"]++
				delete(m.groupHosts[id], hid)
			} else {
				m.calls["ASSOCIATE group host"]++
				m.groupHosts[id][hid] = struct{}{}
			}
			w.WriteHeader(http.StatusNoContent)
		}
	case strings.HasPrefix(tail, "/children/"):
		switch r.Method {
		case "GET":
			m.calls["GET group children"]++
			items := []map[string]any{}
			for cid := range m.groupChild[id] {
				if c, ok := m.groups[cid]; ok {
					items = append(items, c)
				}
			}
			w.Write(listEnvelope(items))
		case "POST":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cid := int64(b["id"].(float64))
			if d, ok := b["disassociate"]; ok && d.(bool) {
				m.calls["DISASSOCIATE group child"]++
				delete(m.groupChild[id], cid)
			} else {
				m.calls["ASSOCIATE group child"]++
				m.groupChild[id][cid] = struct{}{}
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func (m *mockForge) handleJobTemplateNested(w http.ResponseWriter, r *http.Request, path string) {
	id, tail := idFromPath(path, "/api/v2/job_templates/")
	if id < 0 {
		http.NotFound(w, r)
		return
	}
	jt, ok := m.jobTemplates[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case tail == "/" || tail == "":
		switch r.Method {
		case "GET":
			m.calls["GET jobtemplate"]++
			writeJSON(w, http.StatusOK, jt)
		case "PATCH":
			m.calls["PATCH jobtemplate"]++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			for k, v := range b {
				jt[k] = v
			}
			writeJSON(w, http.StatusOK, jt)
		case "DELETE":
			m.calls["DELETE jobtemplate"]++
			delete(m.jobTemplates, id)
			delete(m.jtCreds, id)
			w.WriteHeader(http.StatusNoContent)
		}
	case strings.HasPrefix(tail, "/credentials/"):
		switch r.Method {
		case "GET":
			m.calls["GET jt credentials"]++
			items := []map[string]any{}
			for cid := range m.jtCreds[id] {
				if c, ok := m.credentials[cid]; ok {
					items = append(items, c)
				}
			}
			w.Write(listEnvelope(items))
		case "POST":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			cid := int64(b["id"].(float64))
			if d, ok := b["disassociate"]; ok && d.(bool) {
				m.calls["DISASSOCIATE jt credential"]++
				delete(m.jtCreds[id], cid)
			} else {
				m.calls["ASSOCIATE jt credential"]++
				m.jtCreds[id][cid] = struct{}{}
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// silence unused-symbol warning on `url` import when full file is in flight.
var _ = url.PathEscape
