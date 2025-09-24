package main

import (
	"testing"

	"github.com/gtls"
	"github.com/sky8282/requests"
)

func TestAddType(t *testing.T) {
	session, _ := requests.NewClient(nil, requests.ClientOption{
		DialOption: requests.DialOption{
			AddrType: gtls.Ipv4,
		},
	})
	resp, err := session.Get(nil, "https://test.ipw.cn")
	if err != nil {
		t.Error(err)
	}
	if resp.StatusCode() != 200 {
		t.Errorf("status code error, expected 200, got %d", resp.StatusCode())
	}
}
