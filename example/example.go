package example

import (
	"crypto/tls"
	"github.com/hooklift/gowsdl/example/gen"
	"github.com/hooklift/gowsdl/soap"
	"log"
)

func ExampleBasicUsage() {
	client := soap.NewClient("http://svc.asmx", nil)
	service := gen.NewStockQuotePortType(client)
	reply, err := service.GetLastTradePrice(&gen.TradePriceRequest{})
	if err != nil {
		log.Fatalf("could't get trade prices: %v", err)
	}
	log.Println(reply)
}

func ExampleWithOptions() {
	opts := soap.DefaultOptions()
	opts.BasicAuth = &soap.BasicAuth{
		Login:    "usr",
		Password: "psw",
	}
	opts.TlsConfig = &tls.Config{InsecureSkipVerify: true}

	client := soap.NewClient("http://svc.asmx", &opts)
	service := gen.NewStockQuotePortType(client)
	reply, err := service.GetLastTradePrice(&gen.TradePriceRequest{})
	if err != nil {
		log.Fatalf("could't get trade prices: %v", err)
	}
	log.Println(reply)
}
