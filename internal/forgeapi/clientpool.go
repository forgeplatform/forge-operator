// ClientPool resolves a *forgeapi.Client per ForgeInstance CR.
//
// Multi-cluster sync works by giving each managed CR an optional
// `spec.forgeInstance` field. When set, the controller calls
// `pool.For(ctx, namespace, name)` which:
//   1. fetches the ForgeInstance CR from the manager's k8s cache,
//   2. dereferences `spec.tokenSecretRef` to read the bearer token,
//   3. constructs (and caches) a Client pinned to that URL + token,
//   4. invalidates the cache entry on observed spec changes (via a
//      generation check at lookup time).
//
// When forgeInstance is empty the controller uses the pool's Default
// client, which is the global Forge backend supplied via flags / env.
package forgeapi

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
)

type cachedClient struct {
	generation int64
	client     *Client
}

// ClientPool dispenses per-ForgeInstance clients backed by a k8s reader.
//
// Default is the global / fallback client used when CRs do not reference
// a specific ForgeInstance. K8s is the controller-runtime client used to
// fetch ForgeInstance CRs and referenced Secrets.
type ClientPool struct {
	Default *Client
	K8s     client.Client

	mu    sync.Mutex
	cache map[string]*cachedClient
}

// NewClientPool wires a pool. K8s may be nil for tests that never invoke
// .For() with a non-empty instance name — the default is always returned.
func NewClientPool(def *Client, k client.Client) *ClientPool {
	return &ClientPool{
		Default: def,
		K8s:     k,
		cache:   map[string]*cachedClient{},
	}
}

// For returns a Client for the given ForgeInstance CR (namespace + name).
// Empty name returns the default client.
func (p *ClientPool) For(ctx context.Context, namespace, name string) (*Client, error) {
	if name == "" {
		if p.Default == nil {
			return nil, errors.New("no default Forge client configured")
		}
		return p.Default, nil
	}
	if p.K8s == nil {
		return nil, fmt.Errorf("ClientPool has no k8s reader; cannot resolve ForgeInstance %q", name)
	}

	var fi forgev1.ForgeInstance
	if err := p.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &fi); err != nil {
		return nil, fmt.Errorf("get ForgeInstance %s/%s: %w", namespace, name, err)
	}

	key := namespace + "/" + name
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.cache[key]; ok && entry.generation == fi.Generation {
		return entry.client, nil
	}

	tokenKey := fi.Spec.TokenSecretRef.Key
	if tokenKey == "" {
		tokenKey = "token"
	}
	var sec corev1.Secret
	if err := p.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: fi.Spec.TokenSecretRef.Name}, &sec); err != nil {
		return nil, fmt.Errorf("get token secret %s/%s: %w", namespace, fi.Spec.TokenSecretRef.Name, err)
	}
	raw, ok := sec.Data[tokenKey]
	if !ok {
		return nil, fmt.Errorf("token key %q not present in secret %s/%s", tokenKey, namespace, fi.Spec.TokenSecretRef.Name)
	}

	c := New(fi.Spec.URL, string(raw), fi.Spec.HostHeader, fi.Spec.InsecureSkipVerify)
	p.cache[key] = &cachedClient{generation: fi.Generation, client: c}
	return c, nil
}

// Invalidate drops the cache entry for a given instance — used by the
// ForgeInstance controller after spec changes so the next For() rebuilds.
func (p *ClientPool) Invalidate(namespace, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cache, namespace+"/"+name)
}

// Ping calls /api/v2/ping/ and returns the server version. Used by the
// ForgeInstance reconciler to update status.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var resp struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, "GET", "/api/v2/ping/", nil, &resp); err != nil {
		return "", err
	}
	return resp.Version, nil
}
