package router

import "testing"

// gin 在注册路由时就会因为通配段冲突 panic，这里只要能构造出来就说明路由表是合法的。
func TestNewRegistersRoutes(t *testing.T) {
	engine := New()
	want := map[string]bool{
		"POST /api/canvas/projects":   false,
		"POST /api/user-data/:domain": false,
		"GET /api/user-data/:domain":  false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("route %s not registered", key)
		}
	}
}
