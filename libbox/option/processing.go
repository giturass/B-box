package option

import (
	"reflect"

	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing-box/schema"
)

// Validation lives with the processing engine; the core owns schema generation.
type HTTPRewriteOptions httpdebug.RewriteConfig

func (o *HTTPRewriteOptions) UnmarshalJSON(data []byte) error {
	return (*httpdebug.RewriteConfig)(o).UnmarshalJSON(data)
}

func (o HTTPRewriteOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	node := schema.StrictObject()
	err := builder.FlattenStruct(node, reflect.TypeFor[HTTPRewriteOptions]())
	if err != nil {
		return nil, err
	}
	return node, nil
}

type MITMOptions httpdebug.MITMConfig

func (o *MITMOptions) UnmarshalJSON(data []byte) error {
	return (*httpdebug.MITMConfig)(o).UnmarshalJSON(data)
}
func (o MITMOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	node := schema.StrictObject()
	if err := builder.FlattenStruct(node, reflect.TypeFor[MITMOptions]()); err != nil {
		return nil, err
	}
	return node, nil
}

type ScriptsOptions httpdebug.ScriptsConfig

func (o *ScriptsOptions) UnmarshalJSON(data []byte) error {
	return (*httpdebug.ScriptsConfig)(o).UnmarshalJSON(data)
}
func (o ScriptsOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	node := schema.StrictObject()
	if err := builder.FlattenStruct(node, reflect.TypeFor[ScriptsOptions]()); err != nil {
		return nil, err
	}
	return node, nil
}
