package service

import (
	"log"

	shippingpluginpb "bitbucket.org/lexmodo/proto/shipping_plugin"
)

func logPluginResponse(method string, resp *shippingpluginpb.ResultResponse) {
	if resp == nil {
		log.Printf("%s response: <nil>", method)
		return
	}
	log.Printf("%s response: success=%t failure=%t code=%s message=%q", method, resp.GetSuccess(), resp.GetFailure(), resp.GetCode(), resp.GetMessage())
}
