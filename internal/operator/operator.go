// Package operator implements the CNPG-I operator service, which this
// plugin uses to report its state in the Cluster's status and to validate
// its parameters.
//
// CloudNativePG currently only calls SetStatusInCluster: it never invokes the
// validation RPCs, so invalid parameters can't be rejected when a Cluster is
// admitted. They are reported in the status instead, and the validation RPCs
// are kept for operator versions that do call them.
package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/validation"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"

	"github.com/pganalyze/cnpg-i-pganalyze/internal/config"
	"github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata"
)

// Implementation is the implementation of the operator service
type Implementation struct {
	operator.UnimplementedOperatorServer
	plugin *config.Plugin
}

// NewImplementation creates the operator service for the given settings
func NewImplementation(plugin *config.Plugin) *Implementation {
	return &Implementation{plugin: plugin}
}

// GetCapabilities exposes the operator capabilities
func (Implementation) GetCapabilities(
	context.Context,
	*operator.OperatorCapabilitiesRequest,
) (*operator.OperatorCapabilitiesResult, error) {
	return &operator.OperatorCapabilitiesResult{
		Capabilities: []*operator.OperatorCapability{
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_VALIDATE_CLUSTER_CREATE,
					},
				},
			},
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_VALIDATE_CLUSTER_CHANGE,
					},
				},
			},
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_SET_STATUS_IN_CLUSTER,
					},
				},
			},
		},
	}, nil
}

// ValidateClusterCreate validates the plugin parameters of a new Cluster
func (impl Implementation) ValidateClusterCreate(
	_ context.Context,
	request *operator.OperatorValidateClusterCreateRequest,
) (*operator.OperatorValidateClusterCreateResult, error) {
	errs, err := impl.validate(request.GetDefinition())
	if err != nil {
		return nil, err
	}
	return &operator.OperatorValidateClusterCreateResult{ValidationErrors: errs}, nil
}

// ValidateClusterChange validates the plugin parameters of a changed Cluster
func (impl Implementation) ValidateClusterChange(
	_ context.Context,
	request *operator.OperatorValidateClusterChangeRequest,
) (*operator.OperatorValidateClusterChangeResult, error) {
	errs, err := impl.validate(request.GetNewCluster())
	if err != nil {
		return nil, err
	}
	return &operator.OperatorValidateClusterChangeResult{ValidationErrors: errs}, nil
}

func (impl Implementation) validate(definition []byte) ([]*operator.ValidationError, error) {
	cluster, err := decoder.DecodeClusterLenient(definition)
	if err != nil {
		return nil, err
	}

	helper := common.NewPlugin(*cluster, metadata.PluginName)
	_, problems := config.ParseCluster(impl.plugin, helper.Parameters)

	result := make([]*operator.ValidationError, 0, len(problems))
	for _, problem := range problems {
		result = append(result, validation.BuildErrorForParameter(helper, problem.Name, problem.Message))
	}
	return result, nil
}
