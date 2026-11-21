package threadexec

import "testing"

func TestRegistryRetainsArchivedRecordAndClosesLateRegistration(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	const wire = "550E8400-E29B-41D4-A716-446655440000"
	const canonical = "550e8400-e29b-41d4-a716-446655440000"
	ready := func() bool { return true }

	first := registry.Register(wire, NewRecord(true), ready)
	provisional := registry.BeginResume(canonical, func() bool { return false })
	if provisional != first {
		t.Fatal("alias resume did not acquire the registered record")
	}
	registry.EndResume(canonical, provisional)
	if registry.Latest() != wire {
		t.Fatal("registry lost the handle's original wire spelling")
	}

	registry.Archive(canonical)
	if registry.Latest() != "" || registry.Lookup(wire) != first {
		t.Fatal("archive failed to clear latest or discarded its record")
	}
	registry.CloseRecords()
	if !first.Closed() || registry.Lookup(wire) != first {
		t.Fatal("source loss did not close and retain the record")
	}
	registry.Close()
	late := registry.Register(wire, NewRecord(false), ready)
	if !late.Closed() || registry.Lookup(wire) != nil || registry.Latest() != "" {
		t.Fatal("late registration acquired authority after registry close")
	}
}

func TestRegistryDropsOnlyUnregisteredResumeRecord(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	open := func() bool { return false }
	first := registry.BeginResume("T", open)
	second := registry.BeginResume("T", open)
	if first != second {
		t.Fatal("competing resumes did not share the provisional record")
	}
	registry.EndResume("T", first)
	if registry.Lookup("T") != second {
		t.Fatal("first rejected resume discarded the other observation")
	}
	registry.EndResume("T", second)
	if registry.Lookup("T") != nil {
		t.Fatal("last rejected resume left the provisional record registered")
	}
}
