package main

import (
	"context"
	"testing"

	"github.com/sky8282/requests"
)

func TestHttp2(t *testing.T) {
	resp, err := requests.Get(context.TODO(), "https://httpbin.org/anything")
	if err != nil {
		t.Error(err)
	}
	if resp.StatusCode() != 200 {
		t.Error("resp.StatusCode!= 200")
	}
	if resp.Proto() != "HTTP/2.0" {
		t.Error("resp.Proto!= HTTP/2.0")
	}
	resp, err = requests.Get(context.TODO(), "https://mp.weixin.qq.com")
	if err != nil {
		t.Error(err)
	}
	if resp.StatusCode() != 200 {
		t.Error("resp.StatusCode!= 200")
	}
	if resp.Proto() != "HTTP/2.0" {
		t.Error("resp.Proto!= HTTP/2.0")
	}
}
