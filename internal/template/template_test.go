package template

import "testing"

func TestApply(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		vars map[string]string
		want string
	}{
		{
			name: "year substitution",
			tmpl: "{year}-balance-sheet.txt",
			vars: map[string]string{"year": "2024"},
			want: "2024-balance-sheet.txt",
		},
		{
			name: "basename substitution",
			tmpl: "{basename}.journal",
			vars: map[string]string{"basename": "stmt1"},
			want: "stmt1.journal",
		},
		{
			name: "from_year and to_year",
			tmpl: "{from_year}-{to_year}-tax.txt",
			vars: map[string]string{"from_year": "2023", "to_year": "2024"},
			want: "2023-2024-tax.txt",
		},
		{
			name: "multiple occurrences of same var",
			tmpl: "reports/{year}/{year}-all.journal",
			vars: map[string]string{"year": "2022"},
			want: "reports/2022/2022-all.journal",
		},
		{
			name: "unknown placeholder left unchanged",
			tmpl: "{year}-{unknown}.txt",
			vars: map[string]string{"year": "2024"},
			want: "2024-{unknown}.txt",
		},
		{
			name: "no placeholders",
			tmpl: "reports/all.journal",
			vars: map[string]string{"year": "2024"},
			want: "reports/all.journal",
		},
		{
			name: "empty template",
			tmpl: "",
			vars: map[string]string{"year": "2024"},
			want: "",
		},
		{
			name: "empty vars",
			tmpl: "{year}-balance.txt",
			vars: map[string]string{},
			want: "{year}-balance.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Apply(tt.tmpl, tt.vars)
			if got != tt.want {
				t.Errorf("Apply(%q, %v) = %q, want %q", tt.tmpl, tt.vars, got, tt.want)
			}
		})
	}
}
