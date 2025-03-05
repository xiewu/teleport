package vnet

import (
	"context"
	"errors"
	"strings"

	"github.com/gravitational/teleport/api/client/proto"
	vnetv1 "github.com/gravitational/teleport/gen/proto/go/teleport/lib/vnet/v1"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
)

type localSSHProvider struct {
	ClientApplication
	clusterConfigCache *ClusterConfigCache
}

func newLocalSSHProvider(clientApp ClientApplication, clock clockwork.Clock) *localSSHProvider {
	return &localSSHProvider{
		ClientApplication:  clientApp,
		clusterConfigCache: NewClusterConfigCache(clock),
	}
}

type SSHInfo struct {
	Profile       string
	Cluster       string
	LeafCluster   string
	Hostname      string
	Addr          string
	Ipv4CidrRange string
	DialOptions   *vnetv1.DialOptions
}

func (p *localSSHProvider) ResolveSSHInfo(ctx context.Context, fqdn string) (*SSHInfo, error) {
	if !strings.Contains(fqdn, ".ssh.") {
		return nil, errNoTCPHandler
	}
	profileNames, err := p.ClientApplication.ListProfiles()
	if err != nil {
		return nil, trace.Wrap(err, "listing profiles")
	}
	for _, profileName := range profileNames {
		clusterClient, err := p.clusterClientForFQDN(ctx, profileName, fqdn)
		if err != nil {
			if errors.Is(err, errNoMatch) {
				continue
			}
			// The user might be logged out from this one cluster (and retryWithRelogin isn't working). Log
			// the error but don't return it so that DNS resolution will be forwarded upstream instead of
			// failing.
			log.ErrorContext(ctx, "Failed to get teleport client.", "error", err)
			continue
		}

		leafClusterName := ""
		clusterName := clusterClient.ClusterName()
		if clusterName != "" && clusterName != clusterClient.RootClusterName() {
			leafClusterName = clusterName
		}

		return p.resolveSSHInfoForCluster(ctx, clusterClient, profileName, leafClusterName, fqdn)
	}
	return nil, errNoTCPHandler
}

func (p *localSSHProvider) clusterClientForFQDN(ctx context.Context, profileName, fqdn string) (ClusterClient, error) {
	rootClient, err := p.ClientApplication.GetCachedClient(ctx, profileName, "")
	if err != nil {
		log.ErrorContext(ctx, "Failed to get root cluster client, ssh nodes in this cluster will not be resolved.", "profile", profileName, "error", err)
		return nil, errNoMatch
	}

	if isSSHDescendantSubdomain(fqdn, profileName) {
		return rootClient, nil
	}

	leafClusters, err := getLeafClusters(ctx, rootClient)
	if err != nil {
		// Good chance we're here because the user is not logged in to the profile.
		log.ErrorContext(ctx, "Failed to list leaf clusters, ssh nodes in this cluster will not be resolved.", "profile", profileName, "error", err)
		return nil, errNoMatch
	}

	// Prefix with an empty string to represent the root cluster.
	allClusters := append([]string{""}, leafClusters...)
	for _, leafClusterName := range allClusters {
		clusterClient, err := p.ClientApplication.GetCachedClient(ctx, profileName, leafClusterName)
		if err != nil {
			log.ErrorContext(ctx, "Failed to get cluster client, ssh nodes in this cluster will not be resolved.", "profile", profileName, "leaf_cluster", leafClusterName, "error", err)
			continue
		}

		clusterConfig, err := p.clusterConfigCache.GetClusterConfig(ctx, clusterClient)
		if err != nil {
			log.ErrorContext(ctx, "Failed to get VNet config, ssh nodes in the cluster will not be resolved.", "profile", profileName, "leaf_cluster", leafClusterName, "error", err)
			continue
		}
		for _, zone := range clusterConfig.DNSZones {
			if isSSHDescendantSubdomain(fqdn, zone) {
				return clusterClient, nil
			}
		}
	}
	return nil, errNoMatch
}

func (p *localSSHProvider) resolveSSHInfoForCluster(
	ctx context.Context,
	clusterClient ClusterClient,
	profileName, leafClusterName, fqdn string,
) (*SSHInfo, error) {
	target := stripSSHSuffix(fqdn)
	log := log.With("profile", profileName, "leaf_cluster", leafClusterName, "fqdn", fqdn, "target", target)
	resp, err := clusterClient.CurrentCluster().ResolveSSHTarget(ctx, &proto.ResolveSSHTargetRequest{
		SearchKeywords: []string{target},
	})
	switch {
	case trace.IsNotFound(err):
		return nil, errNoTCPHandler
	case err != nil:
		log.ErrorContext(ctx, "Failed to query SSH node")
		return nil, trace.Wrap(err)
	case resp.GetServer() == nil:
		return nil, errNoTCPHandler
	}
	clusterConfig, err := p.clusterConfigCache.GetClusterConfig(ctx, clusterClient)
	if err != nil {
		log.ErrorContext(ctx, "Failed to get cluster VNet config for matching SSH node", "error", err)
		return nil, trace.Wrap(err, "getting cached cluster VNet config for matching SSH node")
	}
	dialOpts, err := p.ClientApplication.GetDialOptions(ctx, profileName)
	if err != nil {
		log.ErrorContext(ctx, "Failed to get cluster dial options", "error", err)
		return nil, trace.Wrap(err, "getting dial options for matching SSH node")
	}
	return &SSHInfo{
		Profile:       profileName,
		Cluster:       clusterClient.ClusterName(),
		LeafCluster:   leafClusterName,
		Hostname:      resp.GetServer().GetHostname(),
		Addr:          resp.GetServer().GetName() + ":0",
		Ipv4CidrRange: clusterConfig.IPv4CIDRRange,
		DialOptions:   dialOpts,
	}, nil
}

func isSSHDescendantSubdomain(sshFQDN, zone string) bool {
	return strings.HasSuffix(sshFQDN, ".ssh."+fullyQualify(zone))
}

func stripSSHSuffix(s string) string {
	idx := strings.LastIndex(s, ".ssh.")
	if idx == -1 {
		return s
	}
	return s[:idx]
}
