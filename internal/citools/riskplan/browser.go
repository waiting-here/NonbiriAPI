package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Explicit module boundaries connect server changes to browser contracts.
// Shared or unclassified production inputs retain every browser suite.
type browserFamily struct {
	paths  []string
	specs  []string
	suites []string
	engine bool
}

var browserFamilies = []browserFamily{
	{[]string{"internal/gacha/", "internal/appearance/", "web/src/user/activities/gacha/", "web/src/user/features/appearance/", "web/public/assets/gacha/"}, []string{"gacha-", "heart-moment", "appearance-", "rewards", "checkin-choice", "newcomer-rewards"}, []string{"management"}, false},
	{[]string{"internal/game/linklink/", "web/src/user/games/linklink/"}, []string{"linklink-", "game-experience", "game-shell-controls", "desktop-game-layout"}, nil, false},
	{[]string{"internal/fatfish/", "web/src/user/games/fatfish/", "web/public/assets/fatfish/", "web/test/fatfish/", "web/test/fatfish-assets/", "web/test/fatfish-engine/"}, []string{"fatfish-", "game-experience", "desktop-game-layout"}, []string{"fatfish"}, true},
	{[]string{"internal/lakenotes/", "web/src/user/games/lakenotes/", "web/src/user/games/lake-notes/", "web/public/assets/lake-notes/"}, []string{"lake-", "game-experience", "desktop-game-layout"}, []string{"lakenotes"}, false},
	{[]string{"internal/picturebook/", "web/src/user/activities/picturebook/", "web/test/picturebook/"}, []string{"image-", "game-experience"}, []string{"picturebook"}, false},
	{[]string{"internal/riskaudit/", "internal/clientguard/", "web/test/audit/"}, []string{"audit-", "client-scans", "penalties"}, []string{"audit", "management"}, false},
}

var browserSuites = []string{"picturebook", "audit", "management", "fatfish", "lakenotes"}

type browserPlan struct {
	Specs  []string `json:"specs"`
	Suites []string `json:"suites"`
	Engine bool     `json:"engine"`
	// Related sources are relative to web/. Empty means all unit suites.
	Related []string `json:"related"`
}

type browserJob struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Shard string `json:"shard,omitempty"`
}

func (p browserPlan) matrix() []browserJob {
	jobs := []browserJob{}
	if len(p.Specs) > 0 || p.Engine {
		jobs = append(jobs, browserJob{Name: "smoke-1", Kind: "smoke", Shard: "1/2"})
		if len(p.Specs) > 1 {
			jobs = append(jobs, browserJob{Name: "smoke-2", Kind: "smoke", Shard: "2/2"})
		}
	}
	for _, group := range []struct {
		name   string
		suites []string
	}{
		{"content", []string{"picturebook", "fatfish"}},
		{"management", []string{"audit", "management", "lakenotes"}},
	} {
		for _, suite := range group.suites {
			if slices.Contains(p.Suites, suite) {
				jobs = append(jobs, browserJob{Name: group.name, Kind: "integration"})
				break
			}
		}
	}
	return jobs
}

func documentationFile(file string) bool {
	if strings.HasPrefix(file, "docs/") || file == "LICENSE" {
		return true
	}
	if filepath.Dir(file) != "." {
		return false
	}
	switch file {
	case "README.md", "README.zh-CN.md", "CHANGELOG.md", "CONTRIBUTING.md", "SECURITY.md", "SUPPORT.md", "CODE_OF_CONDUCT.md":
		return true
	}
	return false
}

func selectBrowserPlan(plan *verificationPlan) error {
	selected := map[string]bool{}
	suites := map[string]bool{}
	related := map[string]bool{}
	all := plan.Mode == "full"
	unitAll := all
	paths, err := filepath.Glob("web/test/e2e/*.spec.ts")
	if err != nil {
		return err
	}
	catalog := map[string]bool{}
	for _, path := range paths {
		catalog[strings.TrimPrefix(filepath.ToSlash(path), "web/")] = true
	}
	for _, path := range plan.Changed {
		if documentationFile(path) {
			continue
		}
		web := strings.HasPrefix(path, "web/")
		server := strings.HasPrefix(path, "internal/") && !strings.HasSuffix(path, "_test.go") && !strings.Contains(path, "/testdata/")
		fixture := strings.HasPrefix(path, "internal/app/") && strings.Contains(path, "browser")
		if !web && !server && !fixture {
			continue
		}
		// Development-only Go commands have no browser contract.
		if strings.HasPrefix(path, "internal/citools/") {
			continue
		}
		plan.Web = true
		if strings.HasPrefix(path, "web/src/") && (strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) {
			if _, err := os.Stat(path); err == nil {
				related[strings.TrimPrefix(path, "web/")] = true
			} else {
				unitAll = true
			}
		} else {
			unitAll = true
		}
		if strings.HasPrefix(path, "web/test/e2e/") && strings.HasSuffix(path, ".spec.ts") {
			name := strings.TrimPrefix(path, "web/")
			if catalog[name] {
				selected[name] = true
			} else {
				all = true
			}
			continue
		}
		matched := false
		for _, family := range browserFamilies {
			for _, prefix := range family.paths {
				if !strings.HasPrefix(path, prefix) {
					continue
				}
				matched = true
				for spec := range catalog {
					for _, prefix := range family.specs {
						if strings.HasPrefix(filepath.Base(spec), prefix) {
							selected[spec] = true
						}
					}
				}
				for _, suite := range family.suites {
					suites[suite] = true
				}
				plan.Browser.Engine = plan.Browser.Engine || family.engine
			}
		}
		if !matched {
			all = true
		}
	}
	if !plan.Web {
		return nil
	}
	if len(catalog) == 0 {
		return errors.New("browser spec catalog is empty")
	}
	if all {
		selected = catalog
		for _, suite := range browserSuites {
			suites[suite] = true
		}
		plan.Browser.Engine = true
		unitAll = true
		plan.Reasons = append(plan.Reasons, "shared or unclassified browser input retains all browser contracts")
	} else {
		plan.Reasons = append(plan.Reasons, "browser scope includes current module contracts and changed specs")
	}
	plan.Browser.Specs = sortedKeys(selected)
	plan.Browser.Suites = sortedKeys(suites)
	if !unitAll {
		plan.Browser.Related = sortedKeys(related)
	}
	return nil
}
