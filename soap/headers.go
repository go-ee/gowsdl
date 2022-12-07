package soap

import (
	"bytes"
	"encoding/xml"
)

type XmlContent struct {
	Content string
	Items   []interface{}
}

func (o *XmlContent) MarshalXML(e *xml.Encoder, start xml.StartElement) (err error) {
	for _, header := range o.Items {
		if err = e.Encode(header); err != nil {
			return
		}
	}
	return
}

func (o *XmlContent) AddItem(item interface{}) (err error) {
	var headerString string
	switch item.(type) {
	case string:
		headerString = item.(string)
	default:
		buffer := new(bytes.Buffer)
		encoder := xml.NewEncoder(buffer)
		if err = encoder.Encode(item); err != nil {
			return
		}
		headerString = buffer.String()
	}
	o.Content = o.Content + headerString
	o.Items = append(o.Items, item)
	return
}

func (o *XmlContent) SetItems(items []interface{}) (err error) {
	o.Content = ""
	o.Items = items
	for header := range items {
		if err = o.AddItem(header); err != nil {
			return
		}
	}
	return
}

type Header struct {
	XMLName xml.Name    `xml:"soap:Header"`
	Headers *XmlContent `xml:",innerxml"`
}

//func (o *Header) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
//	return e.EncodeElement(o.Headers.Content, start)
//}

type HeaderResponse struct {
	XMLName xml.Name        `xml:"Header"`
	Headers ResponseHeaders `xml:",any"`
}

type ResponseHeaders map[string]interface{}

type HeaderPart struct {
	XMLName xml.Name
	Content string `xml:",chardata"`
}

func (o *ResponseHeaders) UnmarshalXML(d *xml.Decoder, start xml.StartElement) (err error) {
	if *o == nil {
		*o = ResponseHeaders{}
	}

	e := HeaderPart{}
	if err = d.DecodeElement(&e, &start); err != nil {
		return
	}

	(*o)[e.XMLName.Local] = e.Content
	return
}
