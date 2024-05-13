package consts

const (
	NetworkPluginAzure     = "azure"
	NetworkPluginKubenet   = "kubenet"
	
	PodNetworkTypeOverlay  = "overlay"
	PodNetworkTypeNone = ""

	NetworkDataplaneCilium = "cilium"

	// The general idea here is we don't need to allocate secondary ips for host network pods
	// If you bring a podsubnet this happens automatically but in static azure cni we know that
	// kube-proxy and ip-masq-agent are both host network and thus don't need an ip.
	StaticAzureCNIHostNetworkAddons = 2
)
