// Copyright 2020 凯斐德科技（杭州）有限公司 (Karfield Technology, ltd.)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gqlengine

import (
	"fmt"
	"reflect"

	"github.com/karfield/graphql"
)

type Interface interface {
	GraphQLInterfaceDescription() string
}

type NameAlterableInterface interface {
	Interface
	GraphQLInterfaceName() string
}

var interfaceType = reflect.TypeOf((*Interface)(nil)).Elem()

func (engine *Engine) collectInterface(p reflect.Type, prototype Interface) (*graphql.Interface, *unwrappedInfo, error) {
	isInterface, info, err := implementsOf(p, interfaceType)
	if err != nil {
		return nil, &info, err
	}
	if !isInterface {
		return nil, &info, nil
	}

	if i, ok := engine.types[info.baseType]; ok {
		return i.(*graphql.Interface), &info, nil
	}

	if prototype == nil {
		prototype = newPrototype(info.implType).(Interface)
	}

	name := info.baseType.Name()
	if p, ok := prototype.(NameAlterableInterface); ok {
		name = p.GraphQLInterfaceName()
	}

	intf := graphql.NewInterface(graphql.InterfaceConfig{
		Name:        name,
		Description: prototype.GraphQLInterfaceDescription(),
		Fields:      graphql.Fields{},
	})

	engine.types[info.baseType] = intf

	fieldsConfig := objectFieldLazyConfig{
		fields: map[string]objectField{},
	}
	err = engine.objectFields(info.baseType, &fieldsConfig)
	if err != nil {
		return nil, &info, fmt.Errorf("check interface '%s' failed: %E", info.baseType.Name(), err)
	}

	for name, f := range fieldsConfig.fields {
		intf.AddFieldConfig(name, &graphql.Field{
			Name:              f.name,
			Description:       f.desc,
			DeprecationReason: f.deprecated,
			Type:              f.typ,
			// FIXME: need to support args
		})
	}

	return intf, &info, nil
}

func (engine *Engine) asInterfaceFromPrototype(prototype Interface) (*graphql.Interface, error) {
	i, _, err := engine.collectInterface(reflect.TypeOf(prototype), prototype)
	return i, err
}
