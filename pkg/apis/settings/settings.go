/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/url"

	"github.com/go-playground/validator/v10"
	"go.uber.org/multierr"
	v1 "k8s.io/api/core/v1"
	"knative.dev/pkg/configmap"

	coresettings "github.com/aws/karpenter-core/pkg/apis/settings"
)

type settingsKeyType struct{}

var ContextKey = settingsKeyType{}

var defaultSettings = Settings{
	ClusterName:                    "",
	ClusterEndpoint:                "",
	VMMemoryOverheadPercent:        0.075,
	Tags:                           map[string]string{},
	ClusterID:                      "",
	KubeletClientTLSBootstrapToken: "",
	SSHPublicKey:                   "",
	NetworkPlugin:                  "",
	NetworkPolicy:                  "",
}

// +k8s:deepcopy-gen=true
type Settings struct {
	ClusterName             string  `validate:"required"`
	ClusterEndpoint         string  `validate:"required"` // => APIServerName in bootstrap, except needs to be w/o https/port
	VMMemoryOverheadPercent float64 `validate:"min=0"`
	Tags                    map[string]string

	// Cluster-level settings required for nodebootstrap (category "x")
	// (Candidates for exposure/accessibility via API)
	// TODO: consider making these AKS-specific (e.g. subkey?)

	ClusterID string

	KubeletClientTLSBootstrapToken string   `validate:"required"` // => TLSBootstrapToken in bootstrap (may need to be per node/nodepool)
	SSHPublicKey                   string   // ssh.publicKeys.keyData => VM SSH public key // TODO: move to node template?
	NetworkPlugin                  string   `validate:"required"` // => NetworkPlugin in bootstrap
	NetworkPolicy                  string   // => NetworkPolicy in bootstrap
	NodeIdentities                 []string // => Applied onto each VM
}

func (*Settings) ConfigMap() string {
	return "karpenter-global-settings"
}

// Inject creates a Settings from the supplied ConfigMap
func (*Settings) Inject(ctx context.Context, cm *v1.ConfigMap) (context.Context, error) {
	s := defaultSettings.DeepCopy()
	if cm == nil {
		return ToContext(ctx, s), nil
	}

	if err := configmap.Parse(cm.Data,
		configmap.AsString("azure.clusterName", &s.ClusterName),
		configmap.AsString("azure.clusterEndpoint", &s.ClusterEndpoint),
		configmap.AsFloat64("azure.vmMemoryOverheadPercent", &s.VMMemoryOverheadPercent),
		AsStringMap("azure.tags", &s.Tags),
		configmap.AsString("azure.clusterID", &s.ClusterID),
		configmap.AsString("azure.kubeletClientTLSBootstrapToken", &s.KubeletClientTLSBootstrapToken),
		configmap.AsString("azure.sshPublicKey", &s.SSHPublicKey),
		configmap.AsString("azure.networkPlugin", &s.NetworkPlugin),
		configmap.AsString("azure.networkPolicy", &s.NetworkPolicy),
		AsStringSlice("azure.nodeIdentities", &s.NodeIdentities),
	); err != nil {
		return ctx, fmt.Errorf("parsing settings, %w", err)
	}
	if err := s.Validate(); err != nil {
		return ctx, fmt.Errorf("validating settings, %w", err)
	}

	// if clusterID is not set, generate it from cluster endpoint
	if s.ClusterID == "" {
		s.ClusterID = getAKSClusterID(s.GetAPIServerName())
	}

	return ToContext(ctx, s), nil
}

func (s Settings) Data() (map[string]string, error) {
	d := map[string]string{}

	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshaling settings, %w", err)
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("unmarshalling settings, %w", err)
	}
	return d, nil
}

// Validate leverages struct tags with go-playground/validator so you can define a struct with custom
// validation on fields i.e.
//
//	type ExampleStruct struct {
//	    Example  metav1.Duration `json:"example" validate:"required,min=10m"`
//	}
func (s Settings) Validate() error {
	validate := validator.New()
	return multierr.Combine(
		s.validateEndpoint(),
		validate.Struct(s),
	)
}

func (s Settings) validateEndpoint() error {
	endpoint, err := url.Parse(s.ClusterEndpoint)
	// url.Parse() will accept a lot of input without error; make
	// sure it's a real URL
	if err != nil || !endpoint.IsAbs() || endpoint.Hostname() == "" {
		return fmt.Errorf("\"%s\" not a valid clusterEndpoint URL", s.ClusterEndpoint)
	}
	return nil
}

func (s Settings) GetAPIServerName() string {
	endpoint, _ := url.Parse(s.ClusterEndpoint) // already validated
	return endpoint.Hostname()
}

func (*Settings) FromContext(ctx context.Context) coresettings.Injectable {
	return FromContext(ctx)
}

func ToContext(ctx context.Context, s *Settings) context.Context {
	return context.WithValue(ctx, ContextKey, s)
}

func FromContext(ctx context.Context) *Settings {
	data := ctx.Value(ContextKey)
	if data == nil {
		// This is developer error if this happens, so we should panic
		panic("settings doesn't exist in context")
	}
	return data.(*Settings)
}

// AsStringMap parses a value as a JSON map of map[string]string.
func AsStringMap(key string, target *map[string]string) configmap.ParseFunc {
	return func(data map[string]string) error {
		if raw, ok := data[key]; ok {
			m := map[string]string{}
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				return err
			}
			*target = m
		}
		return nil
	}
}

// AsStringMap parses a value as a JSON map of map[string]string.
func AsStringSlice(key string, target *[]string) configmap.ParseFunc {
	return func(data map[string]string) error {
		if raw, ok := data[key]; ok {
			var s []string
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				return err
			}
			*target = s
		}
		return nil
	}
}

// getAKSClusterID returns cluster ID based on the DNS prefix of the cluster.
// The logic comes from AgentBaker and other places, originally from aks-engine
// with the additional assumption of DNS prefix being the first 33 chars of FQDN
func getAKSClusterID(apiServerFQDN string) string {
	dnsPrefix := apiServerFQDN[:33]
	h := fnv.New64a()
	h.Write([]byte(dnsPrefix))
	r := rand.New(rand.NewSource(int64(h.Sum64()))) //nolint:gosec
	return fmt.Sprintf("%08d", r.Uint32())[:8]
}
