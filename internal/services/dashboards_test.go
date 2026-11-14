package services

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var varRef = regexp.MustCompile(`\$\{?([A-Za-z_]+)`)

func TestDashboards_AreWhole(t *testing.T) {
	uids, files := map[string]bool{}, map[string]bool{}
	homes := 0
	for _, d := range dashboards() {
		b := d.board
		uid := b["uid"].(string)
		if uid != d.view.UID || uids[uid] || len(uid) > 40 || files[d.file] || !strings.HasSuffix(d.file, ".json") {
			t.Errorf("%s: uid %q and file %q need to be unique, short, and match its view", d.view.Title, uid, d.file)
		}
		uids[uid], files[d.file] = true, true
		if d.view.Home {
			homes++
			if d.file != "overview.json" {
				t.Errorf("the home dashboard is %s, but Grafana is pointed at overview.json", d.file)
			}
		}
		if d.view.About == "" || b["title"] != d.view.Title {
			t.Errorf("%s: needs an about line and its own title", uid)
		}
		if _, err := json.Marshal(b); err != nil {
			t.Fatalf("%s: %v", uid, err)
		}

		vars := map[string]bool{}
		for _, v := range b["templating"].(map[string]any)["list"].([]any) {
			vars[v.(map[string]any)["name"].(string)] = true
		}
		if d.view.Var != "" && !vars[d.view.Var] {
			t.Errorf("%s: the console links with var-%s, which it does not have", uid, d.view.Var)
		}
		// Every $variable a query or link uses is the dashboard's or Grafana's.
		checkVars := func(where, s string) {
			for _, m := range varRef.FindAllStringSubmatch(s, -1) {
				if !vars[m[1]] && !strings.HasPrefix(m[1], "__") {
					t.Errorf("%s: %s uses $%s, which is not defined", uid, where, m[1])
				}
			}
		}

		ids := map[float64]bool{}
		// Round-trip through JSON, as Grafana reads it.
		var round map[string]any
		data, _ := json.Marshal(b)
		json.Unmarshal(data, &round) //nolint:errcheck
		for _, raw := range round["panels"].([]any) {
			p := raw.(map[string]any)
			title, _ := p["title"].(string)
			id := p["id"].(float64)
			if ids[id] {
				t.Errorf("%s: panel id %v twice", uid, id)
			}
			ids[id] = true
			pos := p["gridPos"].(map[string]any)
			if pos["x"].(float64)+pos["w"].(float64) > 24 {
				t.Errorf("%s / %s runs past the grid: %v", uid, title, pos)
			}
			if p["datasource"].(map[string]any)["uid"] != "${datasource}" {
				t.Errorf("%s / %s: not on the dashboard's data source", uid, title)
			}
			ts, _ := p["targets"].([]any)
			if len(ts) == 0 {
				t.Errorf("%s / %s has no query", uid, title)
			}
			for _, tr := range ts {
				expr, _ := tr.(map[string]any)["expr"].(string)
				if expr == "" || !balanced(expr) {
					t.Errorf("%s / %s: query %q is empty or unbalanced", uid, title, expr)
				}
				checkVars(title, expr)
			}
			if p["type"] == "table" {
				organize := p["transformations"].([]any)[1].(map[string]any)["options"].(map[string]any)
				renamed := organize["renameByName"].(map[string]any)
				for i := range ts {
					if renamed["Value #"+string(rune('A'+i))] == "" {
						t.Errorf("%s / %s: query %d has no column name", uid, title, i)
					}
				}
			}
			checkVars(title+" links", string(mustJSON(p["fieldConfig"])))
		}
	}
	if homes != 1 {
		t.Errorf("%d home dashboards, want 1", homes)
	}
}

func TestDashboards_CoverTheRecipesWithMetrics(t *testing.T) {
	covered := map[string]bool{}
	for _, d := range dashboards() {
		for _, r := range d.view.Recipes {
			if _, ok := RecipeByID(r); !ok {
				t.Errorf("%s is for recipe %q, which does not exist", d.view.UID, r)
			}
			covered[r] = true
		}
	}
	for _, r := range Catalog() {
		if len(r.Metrics) > 0 && !covered[r.ID] {
			t.Errorf("recipe %s has metrics but no dashboard", r.ID)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
