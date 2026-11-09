package labops

import (
	"fmt"

	"astrona/internal/config"
)

// ResourceRef is one script/manifest entry of a lab config and where it is
// configured (e.g. "bootstrap.init[0] setup").
type ResourceRef struct {
	Where string
	Item  config.ResourceItem
}

// ResourceRefs lists every script/manifest entry in cfg with where it's
// configured.
func ResourceRefs(cfg *config.LabConfig) []ResourceRef {
	var refs []ResourceRef
	add := func(where string, items []config.ResourceItem) {
		for i, it := range items {
			refs = append(refs, ResourceRef{fmt.Sprintf("%s[%d] %s", where, i, it.Name), it})
		}
	}
	addBlock := func(prefix string, b config.BootstrapConfig) {
		add(prefix+".init", b.Init)
		add(prefix+".manifests", b.Manifests)
	}
	addValidation := func(prefix string, v config.ValidationConfig) {
		if v.Script != nil {
			add(prefix+".script", []config.ResourceItem{*v.Script})
		}
		add(prefix+".scripts", v.Scripts)
	}

	addBlock("bootstrap", cfg.Bootstrap)
	addBlock("testing", cfg.Testing)
	add("teardown.init", cfg.Teardown.Init)
	addValidation("validation", cfg.Validation)
	for _, l := range cfg.KindClusters() {
		addBlock("runtime.kind.clusters["+l.Name+"].bootstrap", l.Bootstrap)
		addBlock("runtime.kind.clusters["+l.Name+"].testing", l.Testing)
		add("runtime.kind.clusters["+l.Name+"].teardown.init", l.Teardown.Init)
	}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Bootstrap != nil {
			addBlock("runtime.qemu["+vm.Name+"].bootstrap", *vm.Bootstrap)
		}
		if vm.Validation != nil {
			addValidation("runtime.qemu["+vm.Name+"].validation", *vm.Validation)
		}
	}
	return refs
}
