{
	"cniVersion": "0.3.1",
	"name": "kubenet",
	"plugins": [{
		"type": "bridge",
		"bridge": "cbr0",
		"mtu": 1500,
		"addIf": "eth0",
		"isGateway": true,
		"ipMasq": false,
		"promiscMode": true,
		"hairpinMode": false,
		"ipam": {
			"type": "host-local",
			"ranges": [{{range $i, $range := .PodCIDRRanges}}{{if $i}}, {{end}}[{"subnet": "{{$range}}"}]{{end}}],
			"routes": [{{range $i, $route := .Routes}}{{if $i}}, {{end}}{"dst": "{{$route}}"}{{end}}]
		}
	},
	{
		"type": "portmap",
		"capabilities": {"portMappings": true},
		"externalSetMarkChain": "KUBE-MARK-MASQ"
	}]
}
