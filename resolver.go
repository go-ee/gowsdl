package gowsdl

import (
	"bytes"
	"fmt"
	"log"
	"strings"
)

type TypeResolver struct {
	PackageBase                string
	NamespaceToResolver        map[string]*NsTypeResolver
	NamespaceToPackageRelative map[string]string
	NamespaceToPackageFull     map[string]string
	NamespaceToPackage         map[string]string

	namespaceToResolver map[string]*NsTypeResolver
}

func NewTypeResolver(packageBase string) *TypeResolver {
	if packageBase != "" {
		packageBase = packageBase + "/"
	}
	return &TypeResolver{
		PackageBase:                packageBase,
		NamespaceToResolver:        map[string]*NsTypeResolver{},
		NamespaceToPackageRelative: map[string]string{},
		NamespaceToPackageFull:     map[string]string{},
		NamespaceToPackage:         map[string]string{},
		namespaceToResolver:        map[string]*NsTypeResolver{},
	}
}

func (o *TypeResolver) AddNamespace(schema *XSDSchema, nativePackage bool) (ret *NsTypeResolver) {
	namespace := schema.TargetNamespace
	if _, ok := o.NamespaceToPackage[namespace]; !ok {
		o.SetNamespaceToPackage(namespace, nativePackage)
	}
	return NewNsTypeResolver(schema, o, o.NamespaceToPackage[namespace])
}

func (o *TypeResolver) SetNamespaceToPackage(namespace string, nativePackage bool) {
	if !nativePackage {
		namespaceRelative := NamespaceToPackageRelative(namespace)
		o.NamespaceToPackageRelative[namespace] = namespaceRelative
		o.NamespaceToPackageFull[namespace] = fmt.Sprintf("%v%v", o.PackageBase, namespaceRelative)
		o.NamespaceToPackage[namespace] = NamespaceToPackage(namespace)
	} else {
		o.NamespaceToPackageRelative[namespace] = ""
		o.NamespaceToPackageFull[namespace] = ""
		o.NamespaceToPackage[namespace] = ""
	}
}

func (o *TypeResolver) BuildGoType(namespace string, typeName string) (ret string) {
	ret = xsd2GoTypes[strings.ToLower(typeName)]

	if ret == "" {
		ret = replaceReservedWords(makePublic(typeName))
		if namespace != "" {
			goPackage := o.NamespaceToPackage[namespace]
			if goPackage != "" {
				ret = fmt.Sprintf("%v.%v", goPackage, ret)
			} else {
				log.Printf("no package for namespace found: %v, %v", namespace, typeName)
			}
		}
	}
	return
}

func (o *TypeResolver) RegisterTypes(wsdl *WSDL) (ret *NsTypeResolver) {
	xsdTypeResolver := o.AddNamespace(&XSDSchema{TargetNamespace: "http://www.w3.org/2001/XMLSchema", Xmlns: map[string]string{}}, true)
	for k, v := range xsd2GoTypes {
		xsdTypeResolver.RegisterType(k, v)
	}
	// Register types first
	for _, schema := range wsdl.Types.Schemas {
		resolver := o.AddNamespace(schema, false)
		o.namespaceToResolver[schema.TargetNamespace] = resolver
		newTraverser(schema, wsdl.Types.Schemas, resolver).Traverse()
	}

	// Register element types after, because of cycle dependencies
	for _, schema := range wsdl.Types.Schemas {
		newTraverser(schema, wsdl.Types.Schemas, o.namespaceToResolver[schema.TargetNamespace]).Traverse()
	}
	ret = o.namespaceToResolver[wsdl.TargetNamespace]
	if ret == nil {
		ret = o.AddNamespace(&XSDSchema{TargetNamespace: wsdl.TargetNamespace, Xmlns: wsdl.Xmlns}, false)
		o.namespaceToResolver[wsdl.TargetNamespace] = ret
	}

	for _, message := range wsdl.Messages {
		ret.OnMessage(message)
	}
	return
}

func (o *TypeResolver) GetResolverForNamespace(namespace string) *NsTypeResolver {
	return o.namespaceToResolver[namespace]
}

type NsTypeResolver struct {
	Schema           *XSDSchema
	Resolver         *TypeResolver
	NameToGoType     map[string]string
	NameToGoTypeFull map[string]string

	goPackage string
	goImports string
}

func NewNsTypeResolver(schema *XSDSchema, resolver *TypeResolver, goPackage string) (ret *NsTypeResolver) {
	ret = &NsTypeResolver{
		Schema:           schema,
		Resolver:         resolver,
		NameToGoType:     map[string]string{},
		NameToGoTypeFull: map[string]string{}}

	if schema != nil && schema.TargetNamespace != "" {
		resolver.NamespaceToResolver[schema.TargetNamespace] = ret
	} else {
		resolver.NamespaceToResolver[""] = ret
	}
	ret.goPackage = goPackage
	return
}

func (o *NsTypeResolver) GetGoPackage() string {
	return o.goPackage
}

func (o *NsTypeResolver) GetGoImports() string {
	if o.goImports == "" {
		buffer := bytes.Buffer{}
		buffer.WriteString("\"encoding/xml\"\n")
		buffer.WriteString("\"github.com/hooklift/gowsdl/soap\"\n")

		var imp string
		for _, namespace := range o.Schema.Xmlns {
			if o.Schema.TargetNamespace != namespace {
				imp = o.Resolver.NamespaceToPackageFull[namespace]
				if imp != "" {
					buffer.WriteString("\"" + imp + "\"\n")
				}
			}
		}
		o.goImports = buffer.String()
	}
	return o.goImports
}

func (o *NsTypeResolver) ResolveGoType(xsdType string, nillable bool) (ret string) {
	ret = o.findTypeNameFull(xsdType, true)
	if nillable && !isBasicType(ret) {
		ret = "*" + ret
	}
	return
}

func (o *NsTypeResolver) toNamespaceAndType(xsdType string) (namespace string, typeName string) {
	namespaceLabelAndTypeName := strings.Split(xsdType, ":")

	if len(namespaceLabelAndTypeName) == 2 {
		if o.Schema == nil || o.Schema.Xmlns == nil {
			log.Fatalf("can't resolve type '%v' because Schema.Xmlns is null", xsdType)
		}
		namespace = o.Schema.Xmlns[namespaceLabelAndTypeName[0]]
		typeName = namespaceLabelAndTypeName[1]
	} else {
		namespace = o.Schema.TargetNamespace
		typeName = namespaceLabelAndTypeName[0]
	}
	return
}

func (o *NsTypeResolver) OnSimpleType(item *XSDSimpleType) {
	if item.Name != "" {
		o.RegisterType(item.Name, NormalizeTypeName(item.Name))
	}
}

func (o *NsTypeResolver) OnComplexType(item *XSDComplexType) {
	if item.Name != "" {
		o.RegisterType(item.Name, NormalizeTypeName(item.Name))
	}
}

func (o *NsTypeResolver) OnElement(item *XSDElement) {
	if item.ComplexType != nil {
		//log.Printf("register element based complex type %v", item.Name)
		if item.ComplexType.Name != "" {
			o.RegisterType(item.Name, NormalizeTypeName(item.ComplexType.Name))
		} else {
			o.RegisterType(item.Name, NormalizeTypeName(item.Name))
		}
	} else if item.SimpleType != nil {
		log.Printf("register element based simple type %v", item)
	} else {
		//no virtual types to register
	}
	/*
		if item.Name != "" {
			typeNameFull := o.findTypeNameFull(item.Type, false)
			if typeNameFull != "" {
				o.RegisterType(item.Name, typeNameFull)
			} else {
				log.Printf("can't register type for the XSD element: %v", item)
			}
		}*/
}

/*

// Given a message, finds its type.
//
// I'm not very proud of this function but
// it works for now and performance doesn't
// seem critical at this point
func (g *GoWSDL) findType(message string) string {
	message = stripns(message)

	for _, msg := range g.wsdl.Messages {
		if msg.Name != message {
			continue
		}

		// Assumes document/literal wrapped WS-I
		if len(msg.Parts) == 0 {
			// Message does not have parts. This could be a Port
			// with HTTP binding or SOAP 1.2 binding, which are not currently
			// supported.
			log.Printf("[WARN] %s message doesn't have any parts, ignoring message...", msg.Name)
			continue
		}

		part := msg.Parts[0]
		if part.Type != "" {
			return stripns(part.Type)
		}

		elRef := stripns(part.Element)

		for _, schema := range g.wsdl.Types.Schemas {
			for _, el := range schema.Elements {
				if strings.EqualFold(elRef, el.Name) {
					if el.Type != "" {
						return stripns(el.Type)
					}
					return el.Name
				}
			}
		}
	}
	return ""
}
*/

func (o *NsTypeResolver) OnMessage(msg *WSDLMessage) {
	// Assumes document/literal wrapped WS-I
	if len(msg.Parts) == 0 {
		// Message does not have parts. This could be a Port
		// with HTTP binding or SOAP 1.2 binding, which are not currently
		// supported.
		log.Printf("[WARN] %s message doesn't have any parts, ignoring message...", msg.Name)
		return
	}

	part := msg.Parts[0]
	var typeNameFull string
	if part.Type != "" {
		typeNameFull = o.findTypeNameFull(part.Type, false)
	} else {
		typeNameFull = o.findTypeNameFull(part.Element, false)
	}

	if typeNameFull != "" {
		o.RegisterTypeExternal(msg.Name, typeNameFull)
	} else {
		log.Printf("can't register type for the WSDL message port element: %v", part)
	}
}

func (o *NsTypeResolver) findTypeNameFull(nsName string, buildNotAvailable bool) (ret string) {
	namespace, typeName := o.toNamespaceAndType(nsName)
	nsResolver := o.Resolver.NamespaceToResolver[namespace]
	if nsResolver != nil {
		ret = nsResolver.getTypeNameFull(typeName, buildNotAvailable)
	} else if buildNotAvailable {
		ret = o.Resolver.BuildGoType(namespace, typeName)
	}
	return
}

func (o *NsTypeResolver) getTypeNameFull(typeName string, buildNotAvailable bool) (ret string) {
	ret = o.NameToGoTypeFull[typeName]
	if ret == "" && buildNotAvailable {
		ret = o.Resolver.BuildGoType(o.Schema.TargetNamespace, typeName)
	}
	return
}

func (o *NsTypeResolver) RegisterType(name string, typeName string) {
	//log.Printf("register %v: %v", o.Schema.TargetNamespace, name)
	o.NameToGoType[name] = typeName
	if o.goPackage != "" {
		o.NameToGoTypeFull[name] = fmt.Sprintf("%v.%v", o.goPackage, typeName)
	} else {
		o.NameToGoTypeFull[name] = typeName
	}
}

func (o *NsTypeResolver) RegisterTypeExternal(name string, typeName string) {
	//log.Printf("register %v: %v", o.Schema.TargetNamespace, name)
	o.NameToGoType[name] = typeName
	o.NameToGoTypeFull[name] = typeName
}

func NormalizeTypeName(name string) (ret string) {
	ret = makePublic(name)
	return ret
}
