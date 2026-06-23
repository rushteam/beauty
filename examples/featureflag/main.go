// Feature Flag 示例：百分比灰度 + 属性定向 + A/B 实验。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/featureflag"
)

func main() {
	ff := featureflag.New()

	// 20% 灰度；但 plan=pro 的用户强制开启
	ff.SetFlag(featureflag.Flag{
		Key:     "new-ui",
		Enabled: true,
		Rollout: 0.2,
		Rules: []featureflag.Rule{
			{When: map[string]any{"plan": "pro"}, Force: true},
		},
	})

	// A/B 实验：control / treatment 等分
	ff.SetExperiment(featureflag.Experiment{
		Key:      "checkout",
		Variants: []string{"control", "treatment"},
	})

	users := []struct {
		id    string
		attrs featureflag.Attributes
	}{
		{"u-1001", nil},
		{"u-1002", featureflag.Attributes{"plan": "pro"}},
		{"u-1003", nil},
	}
	for _, u := range users {
		variant, _ := ff.Variant("checkout", u.id)
		fmt.Printf("%s  new-ui=%-5v  checkout=%s\n",
			u.id, ff.IsEnabled("new-ui", u.id, u.attrs), variant)
	}
}
