package e2e

import (
	"testing"

	ultra "github.com/aleksclark/ultralogical"
)

func TestA34_GrantNarrowing(t *testing.T) {
	parent := ultra.Grants{Tools: []string{"bash", "view"}, Envs: []ultra.EnvID{"env-a"}, MaySpawn: true, MaxChildren: 2}
	good := ultra.Grants{Tools: []string{"view"}, Envs: []ultra.EnvID{"env-a"}, MaxChildren: 1}
	if !good.SubsetOf(parent) {
		t.Fatal("valid narrowing rejected")
	}
	bad := ultra.Grants{Tools: []string{"terminate_env"}, EnvAll: true, MaySpawn: true, MaxChildren: 3}
	if bad.SubsetOf(parent) {
		t.Fatal("privilege escalation accepted")
	}
}
