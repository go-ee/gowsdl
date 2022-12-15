package soap

type AnyType struct {
	InnerXML string `"xml:",innerxml"`
}

type AnyURI string

type NCName string

type QName string
