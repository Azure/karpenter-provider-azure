version = 2
oom_score = 0
[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "mcr.microsoft.com/oss/kubernetes/pause:3.6"
  [plugins."io.containerd.grpc.v1.cri".containerd]
    {{- if .TeleportConfig.GetStatus }}
    snapshotter = "teleportd"
    disable_snapshot_annotations = false
    {{- else}}
      {{- if .GetIsKata }}
      disable_snapshot_annotations = false
      {{- end}}
    {{- end}}
    {{- if .GetEnableArtifactStreaming }}
    snapshotter = "overlaybd"
    disable_snapshot_annotations = false
    {{- end}}
    {{- if getEnableNvidia . }}
    default_runtime_name = "nvidia-container-runtime"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia-container-runtime]
      runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia-container-runtime.options]
      BinaryName = "/usr/bin/nvidia-container-runtime"
      {{- if .NeedsCgroupv2 }}
      SystemdCgroup = true
      {{- end}}
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.untrusted]
      runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.untrusted.options]
      BinaryName = "/usr/bin/nvidia-container-runtime"
    {{- else}}
    default_runtime_name = "runc"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
      runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
      BinaryName = "/usr/bin/runc"
      {{- if .NeedsCgroupv2 }}
      SystemdCgroup = true
      {{- end}}
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.untrusted]
      runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.untrusted.options]
      BinaryName = "/usr/bin/runc"
    {{- end}}
    {{- if getIsKrustlet .GetWorkloadRuntime }}
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.spin]
      runtime_type = "io.containerd.spin-v0-3-0.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.slight]
      runtime_type = "io.containerd.slight-v0-3-0.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.spin-v0-3-0]
      runtime_type = "io.containerd.spin-v0-3-0.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.slight-v0-3-0]
      runtime_type = "io.containerd.slight-v0-3-0.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.spin-v0-5-1]
      runtime_type = "io.containerd.spin-v0-5-1.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.slight-v0-5-1]
      runtime_type = "io.containerd.slight-v0-5-1.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.spin-v0-8-0]
      runtime_type = "io.containerd.spin-v0-8-0.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.slight-v0-8-0]
      runtime_type = "io.containerd.slight-v0-8-0.v1"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.wws-v0-8-0]
      runtime_type = "io.containerd.wws-v0-8-0.v1"
    {{- end}}
  {{- if getEnsureNoDupePromiscuousBridge .GetNetworkConfig }}
  [plugins."io.containerd.grpc.v1.cri".cni]
    bin_dir = "/opt/cni/bin"
    conf_dir = "/etc/cni/net.d"
    conf_template = "/etc/containerd/kubenet_template.conf"
  {{- end}}
  {{- if isKubernetesVersionGe .GetKubernetesVersion "1.22.0"}}
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
  {{- end}}
  [plugins."io.containerd.grpc.v1.cri".registry.headers]
    X-Meta-Source-Client = ["azure/aks"]
[metrics]
  address = "0.0.0.0:10257"
{{- if .TeleportConfig.GetStatus }}
[proxy_plugins]
  [proxy_plugins.teleportd]
    type = "snapshot"
    address = "/run/teleportd/snapshotter.sock"
{{- end}}
{{- if .GetEnableArtifactStreaming }}
[proxy_plugins]
  [proxy_plugins.overlaybd]
	type = "snapshot"
	address = "/run/overlaybd-snapshotter/overlaybd.sock"
{{- end}}
{{- if .GetIsKata }}
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.katacli]
  runtime_type = "io.containerd.runc.v1"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.katacli.options]
  NoPivotRoot = false
  NoNewKeyring = false
  ShimCgroup = ""
  IoUid = 0
  IoGid = 0
  BinaryName = "/usr/bin/kata-runtime"
  Root = ""
  CriuPath = ""
  SystemdCgroup = false
[proxy_plugins]
  [proxy_plugins.tardev]
    type = "snapshot"
    address = "/run/containerd/tardev-snapshotter.sock"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata-cc]
  snapshotter = "tardev"
  runtime_type = "io.containerd.kata-cc.v2"
  privileged_without_host_devices = true
  pod_annotations = ["io.katacontainers.*"]
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata-cc.options]
    ConfigPath = "/opt/confidential-containers/share/defaults/kata-containers/configuration-clh-snp.toml"
{{- end}}
