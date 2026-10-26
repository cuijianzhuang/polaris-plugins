package polaris

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type extVectors struct {
	Transform struct {
		Request TransformRequest `json:"request"`
		Cases   []struct {
			Name     string          `json:"name"`
			OK       bool            `json:"ok"`
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	} `json:"transform"`
	Metrics struct {
		Now   time.Time `json:"now"`
		Cases []struct {
			OK     bool            `json:"ok"`
			Metric json.RawMessage `json:"metric"`
		} `json:"cases"`
	} `json:"metrics"`
}

func loadExt(t *testing.T) extVectors {
	raw, err := os.ReadFile("../../testdata/extensions.json")
	if err != nil {
		t.Fatal(err)
	}
	var v extVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckTransformVectors(t *testing.T) {
	v := loadExt(t)
	for _, c := range v.Transform.Cases {
		err := CheckTransform(v.Transform.Request, c.Response)
		if (err == nil) != c.OK {
			t.Errorf("%s: %v", c.Name, err)
		}
	}
}

func TestCheckMetricVectors(t *testing.T) {
	v := loadExt(t)
	for _, c := range v.Metrics.Cases {
		// The panel decodes value as a number; a missing or non-number value
		// is rejected before the rules run.
		var probe map[string]json.RawMessage
		json.Unmarshal(c.Metric, &probe)
		var m Metric
		var f float64
		decoded := json.Unmarshal(c.Metric, &m) == nil && json.Unmarshal(probe["value"], &f) == nil
		err := CheckMetric(m, v.Metrics.Now)
		if (decoded && err == nil) != c.OK {
			t.Errorf("%s: decoded=%v err=%v", c.Metric, decoded, err)
		}
	}
}

func TestReportMetrics(t *testing.T) {
	var path string
	var body map[string][]Metric
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		w.Write([]byte(`{"stored":1}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"/api/v1", "t")
	if err := c.ReportMetrics("n/1", []Metric{{Name: "latency", Unit: "ms", Value: 3}}, nil); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v1/nodes/n%2F1/metrics" || len(body["metrics"]) != 1 || body["metrics"][0].Value != 3 {
		t.Fatalf("path=%s body=%v", path, body)
	}
}
