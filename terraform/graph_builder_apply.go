package terraform

import (
	"github.com/hashicorp/terraform/config/module"
	"github.com/hashicorp/terraform/dag"
)

// ApplyGraphBuilder implements GraphBuilder and is responsible for building
// a graph for applying a Terraform diff.
//
// Because the graph is built from the diff (vs. the config or state),
// this helps ensure that the apply-time graph doesn't modify any resources
// that aren't explicitly in the diff. There are other scenarios where the
// diff can be deviated, so this is just one layer of protection.
type ApplyGraphBuilder struct {
	// Module is the root module for the graph to build.
	Module *module.Tree

	// Diff is the diff to apply.
	Diff *Diff

	// State is the current state
	State *State

	// Providers is the list of providers supported.
	Providers []string

	// Provisioners is the list of provisioners supported.
	Provisioners []string

	// DisableReduce, if true, will not reduce the graph. Great for testing.
	DisableReduce bool

	// Destroy, if true, represents a pure destroy operation
	Destroy bool

	// Validate will do structural validation of the graph.
	Validate bool
}

// See GraphBuilder
func (b *ApplyGraphBuilder) Build(path []string) (*Graph, error) {
	return (&BasicGraphBuilder{
		Steps:    b.Steps(),
		Validate: b.Validate,
		Name:     "ApplyGraphBuilder",
	}).Build(path)
}

// See GraphBuilder
func (b *ApplyGraphBuilder) Steps() []GraphTransformer {
	// Custom factory for creating providers.
	concreteProvider := func(a *NodeAbstractProvider) dag.Vertex {
		return &NodeApplyableProvider{
			NodeAbstractProvider: a,
		}
	}

	concreteResource := func(a *NodeAbstractResource) dag.Vertex {
		return &NodeApplyableResource{
			NodeAbstractResource: a,
		}
	}

	steps := []GraphTransformer{
		// Creates all the nodes represented in the diff.
		&DiffTransformer{
			Concrete: concreteResource,

			Diff:   b.Diff,
			Module: b.Module,
			State:  b.State,
		},

		// Create orphan output nodes
		&OrphanOutputTransformer{Module: b.Module, State: b.State},

		// Attach the configuration to any resources
		&AttachResourceConfigTransformer{Module: b.Module},

		// Attach the state
		&AttachStateTransformer{State: b.State},

		// Create all the providers
		&MissingProviderTransformer{Providers: b.Providers, Concrete: concreteProvider},
		&ProviderTransformer{},
		&DisableProviderTransformer{},
		&ParentProviderTransformer{},
		&AttachProviderConfigTransformer{Module: b.Module},

		// Destruction ordering
		&DestroyEdgeTransformer{Module: b.Module, State: b.State},
		GraphTransformIf(
			func() bool { return !b.Destroy },
			&CBDEdgeTransformer{Module: b.Module, State: b.State},
		),

		// Provisioner-related transformations
		&MissingProvisionerTransformer{Provisioners: b.Provisioners},
		&ProvisionerTransformer{},

		// Add root variables
		&RootVariableTransformer{Module: b.Module},

		// Add the outputs
		&OutputTransformer{Module: b.Module},

		// Add module variables
		&ModuleVariableTransformer{Module: b.Module},

		// Connect references so ordering is correct
		&ReferenceTransformer{},

		// Add the node to fix the state count boundaries
		&CountBoundaryTransformer{},

		// Single root
		&RootTransformer{},
	}

	if !b.DisableReduce {
		// Perform the transitive reduction to make our graph a bit
		// more sane if possible (it usually is possible).
		steps = append(steps, &TransitiveReductionTransformer{})
	}

	return steps
}
