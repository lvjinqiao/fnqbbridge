package main

import (
	"net/url"
	"testing"
)

func TestLogin(t *testing.T) {
	for _, s := range []string{"username=admin", "username=admin&password=bad", "username=other&password=a%2Bb%26c"} {
		if _, ok := loginBody([]byte(s), "a+b&c", "real"); ok {
			t.Fatal("accepted invalid login")
		}
	}
	b, ok := loginBody([]byte("username=admin&password=a%2Bb%26c"), "a+b&c", "dynamic&+=")
	v, _ := url.ParseQuery(string(b))
	if !ok || v.Get("password") != "dynamic&+=" {
		t.Fatal("encoding mismatch")
	}
}

func TestDownloaderArgumentParsing(t *testing.T) {
	for _, cmd := range []string{"/usr/bin/trim-qbittorrent-nox\x00--webui-password=a b&+\x00", "trim-qbittorrent-nox\x00--webui-password\x00a b&+\x00"} {
		v, e := getCmdParams(cmd, "webui-password")
		if e != nil || v != "a b&+" {
			t.Fatal("parameter corrupted")
		}
	}
}
