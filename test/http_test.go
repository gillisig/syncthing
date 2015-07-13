// Copyright (C) 2014 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at http://mozilla.org/MPL/2.0/.

// +build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/syncthing/protocol"
	"github.com/syncthing/syncthing/internal/rc"
)

var jsonEndpoints = []string{
	"/rest/db/completion?device=I6KAH76-66SLLLB-5PFXSOA-UFJCDZC-YAOMLEK-CP2GB32-BV5RQST-3PSROAU&folder=default",
	"/rest/db/ignores?folder=default",
	"/rest/db/need?folder=default",
	"/rest/db/status?folder=default",
	"/rest/db/browse?folder=default",
	"/rest/events?since=-1&limit=5",
	"/rest/stats/device",
	"/rest/stats/folder",
	"/rest/svc/deviceid?id=I6KAH76-66SLLLB-5PFXSOA-UFJCDZC-YAOMLEK-CP2GB32-BV5RQST-3PSROAU",
	"/rest/svc/lang",
	"/rest/svc/report",
	"/rest/system/browse?current=.",
	"/rest/system/config",
	"/rest/system/config/insync",
	"/rest/system/connections",
	"/rest/system/discovery",
	"/rest/system/error",
	"/rest/system/ping",
	"/rest/system/status",
	"/rest/system/upgrade",
	"/rest/system/version",
}

func TestGetIndex(t *testing.T) {
	p := startInstance(t, 2)
	defer checkedStop(t, p)

	// Check for explicint index.html

	res, err := http.Get("http://localhost:8082/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Status %d != 200", res.StatusCode)
	}
	bs, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) < 1024 {
		t.Errorf("Length %d < 1024", len(bs))
	}
	if !bytes.Contains(bs, []byte("</html>")) {
		t.Error("Incorrect response")
	}
	if res.Header.Get("Set-Cookie") == "" {
		t.Error("No set-cookie header")
	}
	res.Body.Close()

	// Check for implicit index.html

	res, err = http.Get("http://localhost:8082/")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Status %d != 200", res.StatusCode)
	}
	bs, err = ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) < 1024 {
		t.Errorf("Length %d < 1024", len(bs))
	}
	if !bytes.Contains(bs, []byte("</html>")) {
		t.Error("Incorrect response")
	}
	if res.Header.Get("Set-Cookie") == "" {
		t.Error("No set-cookie header")
	}
	res.Body.Close()
}

func TestGetIndexAuth(t *testing.T) {
	p := startInstance(t, 1)
	defer checkedStop(t, p)

	// Without auth should give 401

	res, err := http.Get("http://127.0.0.1:8081/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Errorf("Status %d != 401", res.StatusCode)
	}

	// With wrong username/password should give 401

	req, err := http.NewRequest("GET", "http://127.0.0.1:8081/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("testuser", "wrongpass")

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("Status %d != 401", res.StatusCode)
	}

	// With correct username/password should succeed

	req, err = http.NewRequest("GET", "http://127.0.0.1:8081/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("testuser", "testpass")

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("Status %d != 200", res.StatusCode)
	}
}

func TestGetJSON(t *testing.T) {
	p := startInstance(t, 2)
	defer checkedStop(t, p)

	for _, path := range jsonEndpoints {
		res, err := http.Get("http://127.0.0.1:8082" + path)
		if err != nil {
			t.Error(path, err)
			continue
		}

		if ct := res.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("Incorrect Content-Type %q for %q", ct, path)
			continue
		}

		var intf interface{}
		err = json.NewDecoder(res.Body).Decode(&intf)
		res.Body.Close()

		if err != nil {
			t.Error(path, err)
		}
	}
}

func TestPOSTWithoutCSRF(t *testing.T) {
	p := startInstance(t, 2)
	defer checkedStop(t, p)

	// Should fail without CSRF

	req, err := http.NewRequest("POST", "http://127.0.0.1:8082/rest/system/error/clear", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("Status %d != 403 for POST", res.StatusCode)
	}

	// Get CSRF

	req, err = http.NewRequest("GET", "http://127.0.0.1:8082/", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	hdr := res.Header.Get("Set-Cookie")
	id := res.Header.Get("X-Syncthing-ID")[:5]
	if !strings.Contains(hdr, "CSRF-Token") {
		t.Error("Missing CSRF-Token in", hdr)
	}

	// Should succeed with CSRF

	req, err = http.NewRequest("POST", "http://127.0.0.1:8082/rest/system/error/clear", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("X-CSRF-Token-"+id, hdr[len("CSRF-Token-"+id+"="):])
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("Status %d != 200 for POST", res.StatusCode)
	}

	// Should fail with incorrect CSRF

	req, err = http.NewRequest("POST", "http://127.0.0.1:8082/rest/system/error/clear", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-CSRF-Token-"+id, hdr[len("CSRF-Token-"+id+"="):]+"X")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("Status %d != 403 for POST", res.StatusCode)
	}
}

func setupAPIBench() *rc.Process {
	err := removeAll("s1", "s2", "h1/index*", "h2/index*")
	if err != nil {
		panic(err)
	}

	err = generateFiles("s1", 25000, 20, "../LICENSE")
	if err != nil {
		panic(err)
	}

	err = ioutil.WriteFile("s1/knownfile", []byte("somedatahere"), 0644)
	if err != nil {
		panic(err)
	}

	// This will panic if there is an actual failure to start, when we try to
	// call nil.Fatal(...)
	return startInstance(nil, 1)
}

func benchmarkURL(b *testing.B, url string) {
	p := setupAPIBench()
	defer p.Stop()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Get(url)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAPI_db_completion(b *testing.B) {
	benchmarkURL(b, "/rest/db/completion?folder=default&device="+protocol.LocalDeviceID.String())
}

func BenchmarkAPI_db_file(b *testing.B) {
	benchmarkURL(b, "/rest/db/file?folder=default&file=knownfile")
}

func BenchmarkAPI_db_ignores(b *testing.B) {
	benchmarkURL(b, "/rest/db/ignores?folder=default")
}

func BenchmarkAPI_db_need(b *testing.B) {
	benchmarkURL(b, "/rest/db/need?folder=default")
}

func BenchmarkAPI_db_status(b *testing.B) {
	benchmarkURL(b, "/rest/db/status?folder=default")
}

func BenchmarkAPI_db_browse_dirsonly(b *testing.B) {
	benchmarkURL(b, "/rest/db/browse?folder=default&dirsonly=true")
}
