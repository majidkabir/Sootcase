package distance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

func ptr[T any](v T) *T { return &v }

// A fake router for the truck profile: routes leaving out "truck" and
// "hzd" are refused unless one of them is dropped; snapping moves 400 m
// when truck is left out, and the nearest road is 20 m away.
func truckServer(t *testing.T, seen *[]string) *httptest.Server {
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ex := r.URL.Query().Get("exclude")
		mu.Lock()
		*seen = append(*seen, r.URL.Path+"?exclude="+ex)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/nearest/") {
			_, _ = io.WriteString(w, `{"code":"Ok","waypoints":[{"distance":20}]}`)
			return
		}
		switch ex {
		case "truck,hzd":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"NoRoute","message":"Impossible route between points"}`)
		case "truck,hzb":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"InvalidValue","message":"Exclude flag combination is not supported."}`)
		case "truck":
			_, _ = io.WriteString(w, `{"code":"Ok","routes":[{"distance":12000}],"waypoints":[{"distance":400},{"distance":10}]}`)
		default:
			_, _ = io.WriteString(w, `{"code":"Ok","routes":[{"distance":10000}],"waypoints":[{"distance":20},{"distance":10}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOSRM_Truck(t *testing.T) {
	var seen []string
	o, err := NewOSRM(truckServer(t, &seen).URL)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := o.Route(context.Background(), calc.Road, copenhagen, hamburg)
	if err != nil || plain.Km != 10 || len(plain.Warnings) != 0 {
		t.Fatalf("no vehicle: %+v, %v", plain, err)
	}
	if !strings.HasSuffix(seen[0], "?exclude=") {
		t.Errorf("no vehicle asked %s; want no exclude", seen[0])
	}

	// A truck leaves out its class and isn't cached; its start was moved
	// 380 m further than the nearest road: a warning.
	ctx := calc.WithVehicle(context.Background(), &calc.Vehicle{HeightM: ptr(4.0)})
	r, err := o.Route(ctx, calc.Road, copenhagen, hamburg)
	if err != nil || r.Km != 12 {
		t.Fatalf("truck: %+v, %v", r, err)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Code != calc.WarnTruckStop || !strings.Contains(r.Warnings[0].Text, "start point") {
		t.Errorf("truck warnings %+v; want the start moved", r.Warnings)
	}

	// Dangerous goods with a tunnel code no route avoids: the class that
	// blocks it is named (without hzd a route is found).
	ctx = calc.WithVehicle(context.Background(), &calc.Vehicle{TunnelCode: "D"})
	_, err = o.Route(ctx, calc.Road, copenhagen, hamburg)
	if !errors.Is(err, calc.ErrNoTruckRoute) || !strings.Contains(err.Error(), "tunnels closed to tunnel code D") {
		t.Errorf("no route: %v; want ErrNoTruckRoute naming code D tunnels", err)
	}

	// A router without the truck classes.
	ctx = calc.WithVehicle(context.Background(), &calc.Vehicle{TunnelCode: "B"})
	if _, err = o.Route(ctx, calc.Road, copenhagen, hamburg); !errors.Is(err, calc.ErrNoTruckRoute) || !strings.Contains(err.Error(), "no truck data") {
		t.Errorf("no truck data: %v", err)
	}

	// Dangerous goods with ferries avoided: no such set.
	ctx = calc.WithRouteOptions(calc.WithVehicle(context.Background(), &calc.Vehicle{TunnelCode: "C"}), calc.RouteOptions{AvoidFerries: true})
	if _, err = o.Route(ctx, calc.Road, copenhagen, hamburg); !errors.Is(err, calc.ErrNoTruckRoute) {
		t.Errorf("hazmat without ferries: %v", err)
	}

	// Too high for the profile.
	ctx = calc.WithVehicle(context.Background(), &calc.Vehicle{HeightM: ptr(4.2)})
	if _, err = o.Route(ctx, calc.Road, copenhagen, hamburg); !errors.Is(err, calc.ErrNoTruckRoute) || !strings.Contains(err.Error(), "height 4.2 m") {
		t.Errorf("4.2 m: %v", err)
	}

	// A truck that avoids ferries leaves out both classes.
	seen = nil
	ctx = calc.WithRouteOptions(calc.WithVehicle(context.Background(), &calc.Vehicle{}), calc.RouteOptions{AvoidFerries: true})
	_, _ = o.Route(ctx, calc.Road, copenhagen, hamburg)
	if len(seen) == 0 || !strings.HasSuffix(seen[0], "?exclude=truck,ferry") {
		t.Errorf("asked %v; want exclude=truck,ferry", seen)
	}
}
