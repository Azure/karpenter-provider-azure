package azure

import (
	"context"
	"os"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func (env *Environment) GetTenantID() string {
	return os.Getenv("AZURE_TENANT_ID")
}

func (env *Environment) GetClusterIdentityPrincipalID(ctx context.Context) string {
	cluster, err := env.managedClusterClient.Get(ctx, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(cluster.Identity).ToNot(BeNil())
	Expect(cluster.Identity.PrincipalID).ToNot(BeNil())
	return lo.FromPtr(cluster.Identity.PrincipalID)
}
