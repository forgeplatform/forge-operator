package forgeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Workflow is the wire-level representation of /api/v2/workflow_job_templates/.
type Workflow struct {
	ID                   int64  `json:"id,omitempty"`
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	Organization         int64  `json:"organization"`
	Inventory            *int64 `json:"inventory,omitempty"`
	AllowSimultaneous    bool   `json:"allow_simultaneous,omitempty"`
	AskInventoryOnLaunch bool   `json:"ask_inventory_on_launch,omitempty"`
	AskVariablesOnLaunch bool   `json:"ask_variables_on_launch,omitempty"`
	AskLimitOnLaunch     bool   `json:"ask_limit_on_launch,omitempty"`
	ExtraVars            string `json:"extra_vars,omitempty"`
}

// WorkflowNode is the wire-level representation of a single node in the
// DAG (/api/v2/workflow_job_template_nodes/).
type WorkflowNode struct {
	ID                 int64  `json:"id,omitempty"`
	WorkflowJobTemplate int64 `json:"workflow_job_template,omitempty"`
	UnifiedJobTemplate int64  `json:"unified_job_template,omitempty"`
	Identifier         string `json:"identifier"`
	ExtraData          string `json:"extra_data,omitempty"`
}

func (c *Client) GetWorkflow(ctx context.Context, id int64) (*Workflow, error) {
	var w Workflow
	if err := c.do(ctx, "GET", "/api/v2/workflow_job_templates/"+strconv.FormatInt(id, 10)+"/", nil, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func (c *Client) FindWorkflowByName(ctx context.Context, name string) (*Workflow, error) {
	q := url.Values{}
	q.Set("name", name)
	q.Set("page_size", "2")

	var lr listResult
	if err := c.do(ctx, "GET", "/api/v2/workflow_job_templates/?"+q.Encode(), nil, &lr); err != nil {
		return nil, err
	}
	if lr.Count == 0 {
		return nil, nil
	}
	if lr.Count > 1 {
		return nil, fmt.Errorf("ambiguous Workflow name %q matched %d records", name, lr.Count)
	}
	var w Workflow
	if err := json.Unmarshal(lr.Results[0], &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func (c *Client) CreateWorkflow(ctx context.Context, w *Workflow) (*Workflow, error) {
	var out Workflow
	if err := c.do(ctx, "POST", "/api/v2/workflow_job_templates/", w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateWorkflow(ctx context.Context, id int64, w *Workflow) (*Workflow, error) {
	var out Workflow
	path := "/api/v2/workflow_job_templates/" + strconv.FormatInt(id, 10) + "/"
	if err := c.do(ctx, "PATCH", path, w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteWorkflow(ctx context.Context, id int64) error {
	err := c.do(ctx, "DELETE", "/api/v2/workflow_job_templates/"+strconv.FormatInt(id, 10)+"/", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ResolveJobTemplate looks up a JobTemplate ID by name. Returns -1 if not found.
func (c *Client) ResolveJobTemplate(ctx context.Context, name string) (int64, error) {
	return c.resolveByName(ctx, "job_templates", name)
}

// ResolveWorkflow looks up a workflow_job_template ID by name.
func (c *Client) ResolveWorkflow(ctx context.Context, name string) (int64, error) {
	return c.resolveByName(ctx, "workflow_job_templates", name)
}

// ListWorkflowNodes returns all nodes belonging to a workflow.
func (c *Client) ListWorkflowNodes(ctx context.Context, workflowID int64) ([]WorkflowNode, error) {
	path := fmt.Sprintf("/api/v2/workflow_job_templates/%d/workflow_nodes/?page_size=200", workflowID)
	var lr listResult
	if err := c.do(ctx, "GET", path, nil, &lr); err != nil {
		return nil, err
	}
	nodes := make([]WorkflowNode, 0, len(lr.Results))
	for _, r := range lr.Results {
		var n WorkflowNode
		if err := json.Unmarshal(r, &n); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// CreateWorkflowNode creates a node within a workflow.
func (c *Client) CreateWorkflowNode(ctx context.Context, workflowID int64, n *WorkflowNode) (*WorkflowNode, error) {
	path := fmt.Sprintf("/api/v2/workflow_job_templates/%d/workflow_nodes/", workflowID)
	var out WorkflowNode
	if err := c.do(ctx, "POST", path, n, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateWorkflowNode(ctx context.Context, nodeID int64, n *WorkflowNode) (*WorkflowNode, error) {
	path := fmt.Sprintf("/api/v2/workflow_job_template_nodes/%d/", nodeID)
	var out WorkflowNode
	if err := c.do(ctx, "PATCH", path, n, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteWorkflowNode(ctx context.Context, nodeID int64) error {
	path := fmt.Sprintf("/api/v2/workflow_job_template_nodes/%d/", nodeID)
	err := c.do(ctx, "DELETE", path, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// AssociateNodeEdge POSTs {"id": targetNodeID} to one of:
//   /workflow_job_template_nodes/{srcID}/success_nodes/
//   /workflow_job_template_nodes/{srcID}/failure_nodes/
//   /workflow_job_template_nodes/{srcID}/always_nodes/
func (c *Client) AssociateNodeEdge(ctx context.Context, srcNodeID int64, edge string, targetNodeID int64) error {
	path := fmt.Sprintf("/api/v2/workflow_job_template_nodes/%d/%s_nodes/", srcNodeID, edge)
	return c.do(ctx, "POST", path, map[string]int64{"id": targetNodeID}, nil)
}

// DisassociateNodeEdge POSTs {"id": targetID, "disassociate": true} to remove a graph edge.
func (c *Client) DisassociateNodeEdge(ctx context.Context, srcNodeID int64, edge string, targetNodeID int64) error {
	path := fmt.Sprintf("/api/v2/workflow_job_template_nodes/%d/%s_nodes/", srcNodeID, edge)
	return c.do(ctx, "POST", path, map[string]any{"id": targetNodeID, "disassociate": true}, nil)
}

// ListNodeEdges returns target node IDs for one edge type on a source node.
func (c *Client) ListNodeEdges(ctx context.Context, srcNodeID int64, edge string) ([]int64, error) {
	path := fmt.Sprintf("/api/v2/workflow_job_template_nodes/%d/%s_nodes/?page_size=200", srcNodeID, edge)
	var lr listResult
	if err := c.do(ctx, "GET", path, nil, &lr); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(lr.Results))
	for _, r := range lr.Results {
		var item struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(r, &item); err != nil {
			return nil, err
		}
		ids = append(ids, item.ID)
	}
	return ids, nil
}
