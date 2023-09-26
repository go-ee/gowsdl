package common

import "reflect"

type Types struct {
	Namespace string
	Types     map[string]reflect.Type
}

func (o *Types) NewInstance(name string) (ret interface{}) {
	return reflect.New(o.Resolve(name)).Elem().Interface()
}

func (o *Types) Resolve(name string) reflect.Type {
	return o.Types[name]
}

func (o *Types) Register(name string, typedNil interface{}) {
	o.Types[name] = reflect.TypeOf(typedNil).Elem()
}

type NamespaceTypes struct {
	Namespaces map[string]*Types
}

func (o *NamespaceTypes) Register(namespace string) (ret *Types) {
	ret = &Types{
		Namespace: namespace,
		Types:     make(map[string]reflect.Type),
	}
	o.Namespaces[namespace] = ret
	return
}

func (o *NamespaceTypes) Resolve(namespace, name string) (ret reflect.Type) {
	if namespaceTypes := o.Namespaces[namespace]; namespaceTypes != nil {
		ret = namespaceTypes.Resolve(name)
	}
	return
}
