package main

import (
	"context"
	"log"
	"testing"

	"github.com/sky8282/ja3"
	"github.com/sky8282/requests"
)

func TestHttp3(t *testing.T) {
	resp, err := requests.Get(context.TODO(), "https://cloudflare-quic.com/", requests.RequestOption{
		ClientOption: requests.ClientOption{
			H3:  true,
			Ja3: true,
		},
	},
	)
	if err != nil {
		t.Error(err)
	}
	log.Print(resp.Proto())
	if resp.StatusCode() != 200 {
		t.Error("resp.StatusCode!= 200")
	}
	if resp.Proto() != "HTTP/3.0" {
		t.Error("resp.Proto!= HTTP/3.0")
	}
}

func TestHttp32(t *testing.T) {
	resp, err := requests.Get(context.TODO(), "https://cloudflare-quic.com/", requests.RequestOption{
		ClientOption: requests.ClientOption{
			UJa3Spec: ja3.CreateUSpecWithId(ja3.QUICChrome_115),
			H3:       true,
		},
	},
	)
	if err != nil {
		t.Error(err)
	}
	log.Print(resp.Proto())
	if resp.StatusCode() != 200 {
		t.Error("resp.StatusCode!= 200")
	}
	if resp.Proto() != "HTTP/3.0" {
		t.Error("resp.Proto!= HTTP/3.0")
	}
}
