package controller

import (
	"context"

	"github.com/forgeplatform/forge-operator/internal/forgeapi"
)

// clientFor returns the right Forge client for a CR's optional
// `spec.forgeInstance` field. When the pool is nil or instanceName is
// empty the fallback (the controller's existing `.Forge` client) is used.
//
// This lets us roll out multi-cluster gradually: CRs that don't set
// `spec.forgeInstance` continue to hit the default backend; CRs that do
// route through ForgeInstance + Secret lookup.
func clientFor(ctx context.Context, pool *forgeapi.ClientPool, fallback *forgeapi.Client, namespace, instanceName string) (*forgeapi.Client, error) {
	if instanceName == "" || pool == nil {
		return fallback, nil
	}
	return pool.For(ctx, namespace, instanceName)
}
