package soap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"
)

type SOAPEncoder interface {
	Encode(v interface{}) error
	Flush() error
}

type SOAPDecoder interface {
	Decode(v interface{}) error
}

type EnvelopeResponse struct {
	XMLName     xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Envelope"`
	Header      *HeaderResponse
	Body        BodyResponse
	Attachments []MIMEMultipartAttachment `xml:"attachments,omitempty"`
}

type Envelope struct {
	XMLName xml.Name `xml:"soap:Envelope"`
	XmlNS   string   `xml:"xmlns:soap,attr"`

	Header *Header
	Body   Body
}

type Body struct {
	XMLName xml.Name `xml:"soap:Body"`

	Content interface{} `xml:",omitempty"`

	// faultOccurred indicates whether the XML body included a fault;
	// we cannot simply store SOAPFault as a pointer to indicate this, since
	// fault is initialized to non-nil with user-provided detail type.
	faultOccurred bool
	Fault         *Fault `xml:",omitempty"`
}

type BodyResponse struct {
	XMLName xml.Name `xml:"Body"`

	Content interface{} `xml:",omitempty"`

	// faultOccurred indicates whether the XML body included a fault;
	// we cannot simply store SOAPFault as a pointer to indicate this, since
	// fault is initialized to non-nil with user-provided detail type.
	faultOccurred bool
	Fault         *Fault `xml:",omitempty"`
}

type MIMEMultipartAttachment struct {
	Name string
	Data []byte
}

// UnmarshalXML unmarshals SOAPBody xml
func (b *BodyResponse) UnmarshalXML(d *xml.Decoder, _ xml.StartElement) error {
	if b.Content == nil {
		return xml.UnmarshalError("Content must be a pointer to a struct")
	}

	var (
		token    xml.Token
		err      error
		consumed bool
	)

Loop:
	for {
		if token, err = d.Token(); err != nil {
			return err
		}

		if token == nil {
			break
		}

		switch se := token.(type) {
		case xml.StartElement:
			if consumed {
				return xml.UnmarshalError("Found multiple elements inside SOAP body; not wrapped-document/literal WS-I compliant")
			} else if se.Name.Space == "http://schemas.xmlsoap.org/soap/envelope/" && se.Name.Local == "Fault" {
				b.Content = nil

				b.faultOccurred = true
				err = d.DecodeElement(b.Fault, &se)
				if err != nil {
					return err
				}

				consumed = true
			} else {
				if err = d.DecodeElement(b.Content, &se); err != nil {
					return err
				}

				consumed = true
			}
		case xml.EndElement:
			break Loop
		}
	}

	return nil
}

func (b *Body) ErrorFromFault() error {
	if b.faultOccurred {
		return b.Fault
	}
	b.Fault = nil
	return nil
}

func (b *BodyResponse) ErrorFromFault() error {
	if b.faultOccurred {
		return b.Fault
	}
	b.Fault = nil
	return nil
}

type DetailContainer struct {
	Detail interface{}
}

type FaultError interface {
	// ErrorString should return a short version of the detail as a string,
	// which will be used in place of <faultstring> for the error message.
	// Set "HasData()" to always return false if <faultstring> error
	// message is preferred.
	ErrorString() string
	// HasData indicates whether the composite fault contains any data.
	HasData() bool
}

type Fault struct {
	XMLName xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault"`

	Code   string     `xml:"faultcode,omitempty"`
	String string     `xml:"faultstring,omitempty"`
	Actor  string     `xml:"faultactor,omitempty"`
	Detail FaultError `xml:"detail,omitempty"`
}

func (f *Fault) Error() string {
	if f.Detail != nil && f.Detail.HasData() {
		return f.Detail.ErrorString()
	}
	return f.String
}

// HTTPError is returned whenever the HTTP request to the server fails
type HTTPError struct {
	//StatusCode is the status code returned in the HTTP response
	StatusCode int
	//ResponseBody contains the body returned in the HTTP response
	ResponseBody []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP Status %d: %s", e.StatusCode, string(e.ResponseBody))
}

const (
	// Predefined WSS namespaces to be used in
	WssNsWSSE       string = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	WssNsWSU        string = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"
	WssNsType       string = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText"
	mtomContentType string = `multipart/related; start-info="application/soap+xml"; type="application/xop+xml"; boundary="%s"`
	XmlNsSoapEnv    string = "http://schemas.xmlsoap.org/soap/envelope/"
)

type WSSSecurityHeader struct {
	XMLName   xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ wsse:Security"`
	XmlNSWsse string   `xml:"xmlns:wsse,attr"`

	MustUnderstand string `xml:"mustUnderstand,attr,omitempty"`

	Token *WSSUsernameToken `xml:",omitempty"`
}

type WSSUsernameToken struct {
	XMLName   xml.Name `xml:"wsse:UsernameToken"`
	XmlNSWsu  string   `xml:"xmlns:wsu,attr"`
	XmlNSWsse string   `xml:"xmlns:wsse,attr"`

	Id string `xml:"wsu:Id,attr,omitempty"`

	Username *WSSUsername `xml:",omitempty"`
	Password *WSSPassword `xml:",omitempty"`
}

type WSSUsername struct {
	XMLName   xml.Name `xml:"wsse:Username"`
	XmlNSWsse string   `xml:"xmlns:wsse,attr"`

	Data string `xml:",chardata"`
}

type WSSPassword struct {
	XMLName   xml.Name `xml:"wsse:Password"`
	XmlNSWsse string   `xml:"xmlns:wsse,attr"`
	XmlNSType string   `xml:"Type,attr"`

	Data string `xml:",chardata"`
}

// NewWSSSecurityHeader creates WSSSecurityHeader instance
func NewWSSSecurityHeader(user, pass, tokenID, mustUnderstand string) *WSSSecurityHeader {
	hdr := &WSSSecurityHeader{XmlNSWsse: WssNsWSSE, MustUnderstand: mustUnderstand}
	hdr.Token = &WSSUsernameToken{XmlNSWsu: WssNsWSU, XmlNSWsse: WssNsWSSE, Id: tokenID}
	hdr.Token.Username = &WSSUsername{XmlNSWsse: WssNsWSSE, Data: user}
	hdr.Token.Password = &WSSPassword{XmlNSWsse: WssNsWSSE, XmlNSType: WssNsType, Data: pass}
	return hdr
}

type BasicAuth struct {
	Login    string
	Password string
}

type Options struct {
	TlsConfig           *tls.Config
	BasicAuth           *BasicAuth
	Timeout             time.Duration
	ConnectionTimeout   time.Duration
	TlsHandShakeTimeout time.Duration
	Client              HTTPClient
	HttpHeaders         map[string]string
	Mtom                bool
	Mma                 bool
	UserAgent           string
	Debug               bool
}

var defaultOptions = Options{
	Timeout:             30 * time.Second,
	ConnectionTimeout:   90 * time.Second,
	TlsHandShakeTimeout: 15 * time.Second,
	UserAgent:           "gowsdl/0.1",
}

func DefaultOptions() Options {
	return defaultOptions
}

func (o *Options) BuildHttpClient() (ret *http.Client, err error) {
	tr := &http.Transport{
		TLSClientConfig: o.TlsConfig,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: o.Timeout}
			return d.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout: o.TlsHandShakeTimeout,
	}
	var jar *cookiejar.Jar
	if jar, err = cookiejar.New(nil); err != nil {
		return
	}
	ret = &http.Client{Timeout: o.ConnectionTimeout, Transport: tr, Jar: jar}
	return
}

func (o *Options) getOrBuildHttpClient() (ret HTTPClient, err error) {
	if o.Client == nil {
		o.Client, err = o.BuildHttpClient()
	}
	ret = o.Client
	return
}

// Client is soap Client
type Client struct {
	Headers     *XmlContent
	url         string
	opts        *Options
	attachments []MIMEMultipartAttachment
}

// HTTPClient is a Client which can make HTTP requests
// An example implementation is net/http.Client
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewClient creates new SOAP Client instance
func NewClient(url string, opts *Options) *Client {
	if opts == nil {
		defOpts := DefaultOptions()
		opts = &defOpts
	}
	return &Client{
		url:  url,
		opts: opts,
	}
}

// AddMIMEMultipartAttachment adds an attachment to the Client that will be sent only if the
// WithMIMEMultipartAttachments option is used
func (s *Client) AddMIMEMultipartAttachment(attachment MIMEMultipartAttachment) {
	s.attachments = append(s.attachments, attachment)
}

// CallContext performs HTTP POST request with a context
func (s *Client) CallContext(ctx context.Context, soapAction string, request interface{}, responseHeader map[string]interface{},
	responseContent interface{}, headers map[string]string) error {
	return s.call(ctx, soapAction, request, responseHeader, responseContent, nil, nil, headers)
}

// Call performs HTTP POST request.
// Note that if the server returns a status code >= 400, a HTTPError will be returned
func (s *Client) Call(soapAction string, request interface{}, responseHeader map[string]interface{}, responseContent interface{},
	headers map[string]string) error {
	return s.call(context.Background(), soapAction, request, responseHeader, responseContent, nil, nil, headers)
}

// CallContextWithAttachmentsAndFaultDetail performs HTTP POST request.
// Note that if SOAP fault is returned, it will be stored in the error.
// On top the attachments array will be filled with attachments returned from the SOAP request.
func (s *Client) CallContextWithAttachmentsAndFaultDetail(ctx context.Context, soapAction string, request interface{},
	responseHeader map[string]interface{}, responseContent interface{}, faultDetail FaultError,
	attachments *[]MIMEMultipartAttachment, headers map[string]string) error {
	return s.call(ctx, soapAction, request, responseHeader, responseContent, faultDetail, attachments, headers)
}

// CallContextWithFault performs HTTP POST request.
// Note that if SOAP fault is returned, it will be stored in the error.
func (s *Client) CallContextWithFaultDetail(ctx context.Context, soapAction string, request,
	responseHeader map[string]interface{}, responseContent interface{}, faultDetail FaultError, headers map[string]string) error {
	return s.call(ctx, soapAction, request, responseHeader, responseContent, faultDetail, nil, headers)
}

// CallWithFaultDetail performs HTTP POST request.
// Note that if SOAP fault is returned, it will be stored in the error.
// the passed in fault detail is expected to implement FaultError interface,
// which allows to condense the detail into a short error message.
func (s *Client) CallWithFaultDetail(soapAction string, request interface{},
	responseHeader map[string]interface{}, responseContent interface{}, faultDetail FaultError, headers map[string]string) error {
	return s.call(context.Background(), soapAction, request, responseHeader, responseContent, faultDetail, nil, headers)
}

func (s *Client) call(ctx context.Context, soapAction string, request interface{}, responseHeader map[string]interface{},
	responseContent interface{}, faultDetail FaultError, retAttachments *[]MIMEMultipartAttachment, headers map[string]string) (err error) {

	// SOAP envelope capable of namespace prefixes
	envelope := Envelope{
		XmlNS: XmlNsSoapEnv,
	}

	if s.Headers != nil {
		envelope.Header = &Header{
			Headers: s.Headers,
		}
	}

	envelope.Body.Content = request
	buffer := new(bytes.Buffer)
	buffer.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	var encoder SOAPEncoder
	if s.opts.Mtom && s.opts.Mma {
		return fmt.Errorf("cannot use MTOM (XOP) and MMA (MIME Multipart Attachments) option at the same time")
	} else if s.opts.Mtom {
		encoder = newMtomEncoder(buffer)
	} else if s.opts.Mma {
		encoder = newMmaEncoder(buffer, s.attachments)
	} else {
		encoder = xml.NewEncoder(buffer)
	}

	if err = encoder.Encode(envelope); err != nil {
		return
	}

	if err = encoder.Flush(); err != nil {
		return
	}

	var req *http.Request
	if req, err = http.NewRequest("POST", s.url, buffer); err != nil {
		return
	}
	if s.opts.BasicAuth != nil {
		req.SetBasicAuth(s.opts.BasicAuth.Login, s.opts.BasicAuth.Password)
	}

	req = req.WithContext(ctx)

	if s.opts.Mtom {
		req.Header.Add("Content-Type", fmt.Sprintf(mtomContentType, encoder.(*mtomEncoder).Boundary()))
	} else if s.opts.Mma {
		req.Header.Add("Content-Type", fmt.Sprintf(mmaContentType, encoder.(*mmaEncoder).Boundary()))
	} else {
		req.Header.Add("Content-Type", "text/xml; charset=\"utf-8\"")
	}
	req.Header.Add("SOAPAction", soapAction)
	req.Header.Set("User-Agent", s.opts.UserAgent)
	if s.opts.HttpHeaders != nil {
		for k, v := range s.opts.HttpHeaders {
			req.Header.Set(k, v)
		}
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	req.Close = true

	var client HTTPClient
	if client, err = s.opts.getOrBuildHttpClient(); err != nil {
		return
	}

	if s.opts.Debug {
		fmt.Printf("\n=== Start: Debug Request ===\n")
		fmt.Printf("\nrequest: body=%v, header=%v\n", buffer.String(), req.Header)
		fmt.Printf("\n=== End: Debug Request===\n")
	}

	var res *http.Response
	if res, err = client.Do(req); err != nil {
		return
	}
	defer res.Body.Close()

	bodyReader := res.Body
	if s.opts.Debug {
		fmt.Printf("\n=== Start: Debug Response ===\n")
		buf := new(bytes.Buffer)
		_, err = buf.ReadFrom(bodyReader)
		bodyReader = io.NopCloser(bytes.NewReader(buf.Bytes()))

		fmt.Printf("\nresponse: body=%v, header=%v\n", buf.String(), res.Header)

		//spew.Dump("SOAP Response: ", res)
		//fmt.Printf("Response.Body: %v", buf.String())
		//bodyReader = io.NopCloser(bytes.NewReader(buf.Bytes()))

		//mapDecoder := xml2map.NewDecoder(strings.NewReader(buf.String()))
		//responseMap, mapErr := mapDecoder.Decode()
		//fmt.Printf("response: %v, err: %v", responseMap, mapErr)

		fmt.Printf("\n=== End: Debug Response===\n")
	}

	// xml Decoder (used with and without MTOM) cannot handle namespace prefixes (yet),
	// so we have to use a namespace-less response envelope
	respEnvelope := new(EnvelopeResponse)
	respEnvelope.Header = &HeaderResponse{
		Headers: responseHeader,
	}
	//respEnvelope.Header.ResponseHeaders = append(respEnvelope.Header.ResponseHeaders, responseHeader)
	respEnvelope.Body = BodyResponse{
		Content: responseContent,
		Fault: &Fault{
			Detail: faultDetail,
		},
	}

	var mtomBoundary string
	contentType := res.Header.Get("Content-Type")
	if mtomBoundary, err = getMtomHeader(contentType); err != nil {
		return
	}

	var mmaBoundary string
	if s.opts.Mma {
		if mmaBoundary, err = getMmaHeader(contentType); err != nil {
			return
		}
	}

	var dec SOAPDecoder
	if mtomBoundary != "" {
		dec = newMtomDecoder(bodyReader, mtomBoundary)
	} else if mmaBoundary != "" {
		dec = newMmaDecoder(bodyReader, mmaBoundary)
	} else {
		dec = xml.NewDecoder(bodyReader)
	}

	if err = dec.Decode(respEnvelope); err != nil {
		return err
	}

	if respEnvelope.Attachments != nil {
		*retAttachments = respEnvelope.Attachments
	}
	return respEnvelope.Body.ErrorFromFault()
}
