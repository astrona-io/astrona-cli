package version

import "testing"

func TestParseAndCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.2.1", "0.2.1", 0},
		{"0.2.1", "0.2.2", -1},
		{"0.10.0", "0.9.9", 1},
		{"0.3.0-rc1", "0.3.0", -1},
		{"0.3.0-rc1", "0.3.0-rc2", -1},
		{"0.3", "0.3.0", 0},
		{"1.0.0", "0.99.99", 1},
	} {
		a, err := Parse(c.a)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Parse(c.b)
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"developer", "", "1", "v1.x.0", "0.2.1;rm"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestConstraint(t *testing.T) {
	for _, c := range []struct {
		constraint, version string
		want                bool
	}{
		{"<=0.2.1", "0.2.1", true},
		{"<=0.2.1", "0.2.2", false},
		{"<=0.2.1", "0.2.0", true},
		{">=0.2.0, <0.3.0", "0.2.9", true},
		{">=0.2.0, <0.3.0", "0.3.0", false},
		{">=0.2.0, <0.3.0", "0.3.0-rc1", true},
		{"0.2.1", "0.2.1", true},
		{"0.2.1", "0.2.2", false},
		{"!=0.2.2", "0.2.3", true},
		{"> 0.2.0", "0.2.1", true},
	} {
		con, err := ParseConstraint(c.constraint)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := Parse(c.version)
		if got := con.Allows(v); got != c.want {
			t.Errorf("%q allows %s = %v", c.constraint, c.version, got)
		}
	}
	for _, bad := range []string{"", ">=", "~0.2", ">=0.2.0,", "latest"} {
		if _, err := ParseConstraint(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
