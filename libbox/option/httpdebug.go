package option

import (
	"reflect"

	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing-box/schema"
)

// Keep the engine independent of sing-box while exposing its validated JSON
// object through the matching core's custom-type schema interface.
type HTTPDebugOptions httpdebug.Config

func (o *HTTPDebugOptions) UnmarshalJSON(data []byte) error {
	return (*httpdebug.Config)(o).UnmarshalJSON(data)
}

func (HTTPDebugOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("HTTPDebugOptions", func() (*schema.Node, error) {
		node := schema.StrictObject()
		if err := builder.FlattenStruct(node, reflect.TypeFor[httpdebug.Config]()); err != nil {
			return nil, err
		}
		return node, nil
	})
}
