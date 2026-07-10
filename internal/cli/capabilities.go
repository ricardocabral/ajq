package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ricardocabral/ajq/internal/semantics"
	"github.com/ricardocabral/ajq/internal/version"
	"github.com/spf13/cobra"
)

// capabilitiesDocument is the deterministic v1 capabilities wire contract.
// Apart from the informational ajq_version build value, all documented fields
// are stable for schema_version "1". Consumers must ignore unknown fields and
// enum values added by compatible future builds. This document is constructed
// solely from compiled metadata and never resolves configuration or creates a
// semantic backend.
type capabilitiesDocument struct {
	SchemaVersion     string                       `json:"schema_version"`
	AJQVersion        string                       `json:"ajq_version"`
	InputModes        []capabilityInputMode        `json:"input_modes"`
	OutputModes       []capabilityOutputMode       `json:"output_modes"`
	SemanticFunctions []capabilitySemanticFunction `json:"semantic_functions"`
	Backends          []capabilityBackend          `json:"backends"`
	Cost              capabilityCost               `json:"cost"`
	Cache             capabilityCache              `json:"cache"`
	Provisioning      capabilityProvisioning       `json:"provisioning"`
	Safety            capabilitySafety             `json:"safety"`
	Discovery         capabilityDiscovery          `json:"discovery"`
}

type capabilityInputMode struct {
	Name      string `json:"name"`
	Selection string `json:"selection"`
	Streaming bool   `json:"streaming"`
}

type capabilityOutputMode struct {
	Format     string `json:"format"`
	Style      string `json:"style"`
	Flag       string `json:"flag,omitempty"`
	Default    bool   `json:"default"`
	StringOnly bool   `json:"string_only"`
}

type capabilitySemanticFunction struct {
	Name         string                 `json:"name"`
	Kind         string                 `json:"kind"`
	ReturnType   string                 `json:"return_type"`
	Availability capabilityAvailability `json:"availability"`
}

type capabilityAvailability struct {
	Status                     string   `json:"status"`
	SupportedContexts          []string `json:"supported_contexts"`
	UnsupportedContextBehavior string   `json:"unsupported_context_behavior"`
	Limitations                []string `json:"limitations"`
}

type capabilityBackend struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	NeedsModel             bool   `json:"needs_model"`
	NeedsBaseURL           bool   `json:"needs_base_url"`
	Paid                   bool   `json:"paid"`
	DefaultMaxCalls        int    `json:"default_max_calls"`
	APIKeyEnv              string `json:"api_key_env,omitempty"`
	DefaultModel           string `json:"default_model,omitempty"`
	DefaultBaseURL         string `json:"default_base_url,omitempty"`
	DefaultMaxOutputTokens int    `json:"default_max_output_tokens,omitempty"`
}

type capabilityCost struct {
	PaidDefaultMaxCalls int      `json:"paid_default_max_calls"`
	UnlimitedMaxCalls   int      `json:"unlimited_max_calls"`
	OverrideSources     []string `json:"override_sources"`
}

type capabilityCache struct {
	EnabledByDefault   bool     `json:"enabled_by_default"`
	IdentityComponents []string `json:"identity_components"`
}

type capabilityProvisioning struct {
	LocalBackend string `json:"local_backend"`
	Command      string `json:"command"`
}

type capabilitySafety struct {
	PureJQ            capabilityPureJQSafety    `json:"pure_jq"`
	SemanticExecution capabilitySemanticSafety  `json:"semantic_execution"`
	MockBackend       capabilityMockBackendSafe `json:"mock_backend"`
}

type capabilityPureJQSafety struct {
	ConstructsBackend bool `json:"constructs_backend"`
	StartsDaemon      bool `json:"starts_daemon"`
	MakesNetworkCall  bool `json:"makes_network_call"`
}

type capabilitySemanticSafety struct {
	RequiresExplicitOperator bool `json:"requires_explicit_operator"`
	RequiresSelectedBackend  bool `json:"requires_selected_backend"`
}

type capabilityMockBackendSafe struct {
	Deterministic    bool `json:"deterministic"`
	InProcess        bool `json:"in_process"`
	MakesNetworkCall bool `json:"makes_network_call"`
}

type capabilityDiscovery struct {
	ExamplesCommand string `json:"examples_command"`
}

func newCapabilitiesCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:           "capabilities",
		Short:         "print ajq capability metadata",
		Long:          "Print static ajq capability metadata. The human summary is informational; use --json for the versioned machine-readable contract.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if jsonOutput {
				err = json.NewEncoder(cmd.OutOrStdout()).Encode(newCapabilitiesDocument())
			} else {
				err = writeCapabilitiesSummary(cmd)
			}
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write capabilities: %w", err)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the versioned machine-readable capabilities contract")
	return cmd
}

func writeCapabilitiesSummary(cmd *cobra.Command) error {
	document := newCapabilitiesDocument()
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "ajq capabilities (informational; use --json for the stable v%s contract)\nsemantic functions: %d\nbackends: %d\nexamples: %s\n", document.SchemaVersion, len(document.SemanticFunctions), len(document.Backends), document.Discovery.ExamplesCommand)
	return err
}

func newCapabilitiesDocument() capabilitiesDocument {
	return capabilitiesDocument{
		SchemaVersion: "1",
		AJQVersion:    version.Version,
		InputModes: []capabilityInputMode{
			{Name: "json", Selection: "auto", Streaming: true},
			{Name: "null", Selection: "--null-input", Streaming: false},
			{Name: "raw", Selection: "--raw-input", Streaming: true},
		},
		OutputModes: []capabilityOutputMode{
			{Format: "json", Style: "pretty", Default: true, StringOnly: false},
			{Format: "json", Style: "compact", Flag: "--compact-output", Default: false, StringOnly: false},
			{Format: "string", Style: "raw", Flag: "--raw-output", Default: false, StringOnly: true},
		},
		SemanticFunctions: capabilitySemanticFunctions(),
		Backends:          capabilityBackends(),
		Cost: capabilityCost{
			PaidDefaultMaxCalls: 100,
			UnlimitedMaxCalls:   0,
			OverrideSources:     []string{"flag", "environment", "config"},
		},
		Cache: capabilityCache{
			EnabledByDefault:   true,
			IdentityComponents: []string{"backend", "model", "spec", "canonical_value"},
		},
		Provisioning: capabilityProvisioning{LocalBackend: "managed_optional", Command: "ajq provision"},
		Safety: capabilitySafety{
			PureJQ:            capabilityPureJQSafety{ConstructsBackend: false, StartsDaemon: false, MakesNetworkCall: false},
			SemanticExecution: capabilitySemanticSafety{RequiresExplicitOperator: true, RequiresSelectedBackend: true},
			MockBackend:       capabilityMockBackendSafe{Deterministic: true, InProcess: true, MakesNetworkCall: false},
		},
		Discovery: capabilityDiscovery{ExamplesCommand: "ajq examples"},
	}
}

func capabilitySemanticFunctions() []capabilitySemanticFunction {
	operations := semantics.All()
	functions := make([]capabilitySemanticFunction, 0, len(operations))
	for _, operation := range operations {
		operationContexts := operation.Availability.SupportedContexts()
		contexts := make([]string, len(operationContexts))
		for i, context := range operationContexts {
			contexts[i] = string(context)
		}
		operationLimitations := operation.Availability.Limitations()
		limitations := make([]string, len(operationLimitations))
		for i, limitation := range operationLimitations {
			limitations[i] = string(limitation)
		}
		functions = append(functions, capabilitySemanticFunction{
			Name:       operation.Name,
			Kind:       string(operation.Kind),
			ReturnType: string(operation.Return),
			Availability: capabilityAvailability{
				Status:                     string(operation.Availability.Status),
				SupportedContexts:          contexts,
				UnsupportedContextBehavior: "fails_loudly",
				Limitations:                limitations,
			},
		})
	}
	return functions
}

func capabilityBackends() []capabilityBackend {
	backends := make([]capabilityBackend, 0, len(backendRegistry))
	for _, registration := range backendRegistry {
		backends = append(backends, capabilityBackend{
			Name:                   registration.Name,
			Description:            registration.HelpDescriptor,
			NeedsModel:             registration.NeedsModel,
			NeedsBaseURL:           registration.NeedsBaseURL,
			Paid:                   registration.Paid,
			DefaultMaxCalls:        registration.DefaultMaxCalls,
			APIKeyEnv:              registration.APIKeyEnv,
			DefaultModel:           registration.DefaultModel,
			DefaultBaseURL:         registration.DefaultBaseURL,
			DefaultMaxOutputTokens: registration.DefaultMaxOutputTokens,
		})
	}
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	return backends
}
