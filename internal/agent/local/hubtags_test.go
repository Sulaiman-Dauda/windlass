package local

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestHubTags(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("name") == "1.2":
			fmt.Fprint(w, `{"results":[{"name":"1.2.1"}]}`)
		case r.URL.Path == "/v2/namespaces/library/repositories/nginx/tags" && r.URL.Query().Get("page") == "":
			fmt.Fprintf(w, `{"next":%q,"results":[{"name":"1.27"},{"name":"1.26"}]}`, srv.URL+"/v2/namespaces/library/repositories/nginx/tags?page=2")
		case r.URL.Path == "/v2/namespaces/library/repositories/nginx/tags" && r.URL.Query().Get("page") == "2":
			// A next link off the API host is not followed.
			fmt.Fprint(w, `{"next":"https://elsewhere.example/x","results":[{"name":"1.25"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := hubAPI
	hubAPI = srv.URL
	defer func() { hubAPI = old }()

	d := dockerLocal{}
	tags, err := d.HubTags(context.Background(), "library/nginx", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1.27", "1.26", "1.25"}; !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	if _, err := d.HubTags(context.Background(), "acme/private", ""); err != errHubNotFound {
		t.Fatalf("missing repo err = %v", err)
	}
	if tags, err := d.HubTags(context.Background(), "library/nginx", "1.2"); err != nil || !reflect.DeepEqual(tags, []string{"1.2.1"}) {
		t.Fatalf("contains = %v, %v", tags, err)
	}
	for _, bad := range []string{"nginx", "a/b/c", "/x"} {
		if _, err := d.HubTags(context.Background(), bad, ""); err == nil {
			t.Errorf("HubTags(%q) accepted", bad)
		}
	}
}
