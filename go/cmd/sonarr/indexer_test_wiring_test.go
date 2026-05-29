package main

import (
	"context"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/provider"
)

// fakeConcreteIndexer is a concreteIndexer stub for adapter tests.
type fakeConcreteIndexer struct {
	result    provider.ValidationResult
	actionRet any
}

func (f fakeConcreteIndexer) Test(context.Context) provider.ValidationResult { return f.result }
func (f fakeConcreteIndexer) RequestAction(_ string, _ map[string]string) (any, error) {
	return f.actionRet, nil
}
func (f fakeConcreteIndexer) SetDefinition(*provider.Definition) {}

func TestConcreteIndexerProviderAdapter_TestTranslatesErrorsOnly(t *testing.T) {
	t.Parallel()
	res := provider.ValidationResult{Errors: []provider.ValidationFailure{
		{PropertyName: "baseUrl", Message: "bad url", Severity: provider.MessageError},
		{PropertyName: "", Message: "just a warning", Severity: provider.MessageWarning},
		{PropertyName: "apiKey", Message: "missing", Severity: provider.MessageError, DetailedDescription: "set it"},
	}}
	a := concreteIndexerProviderAdapter{idx: fakeConcreteIndexer{result: res}}
	got, err := a.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Only the 2 error-severity failures translate; the warning is skipped.
	if len(got) != 2 {
		t.Fatalf("got %d failures, want 2 (warnings skipped): %+v", len(got), got)
	}
	if got[0].PropertyName != "baseUrl" || got[0].ErrorMessage != "bad url" {
		t.Errorf("failure[0] = %+v", got[0])
	}
	if got[1].ErrorMessage != "missing" || got[1].DetailedDescription != "set it" {
		t.Errorf("failure[1] = %+v", got[1])
	}
}

func TestConcreteIndexerProviderAdapter_ValidResultEmptyFailures(t *testing.T) {
	t.Parallel()
	a := concreteIndexerProviderAdapter{idx: fakeConcreteIndexer{result: provider.ValidResult()}}
	got, err := a.Test(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("valid result → no failures; got %d err %v", len(got), err)
	}
}

func TestConcreteIndexerProviderAdapter_RequestActionDelegates(t *testing.T) {
	t.Parallel()
	a := concreteIndexerProviderAdapter{idx: fakeConcreteIndexer{actionRet: "ok"}}
	got, err := a.RequestAction(context.Background(), "newznabCategories", nil)
	if err != nil || got != "ok" {
		t.Fatalf("RequestAction delegate: got %v err %v", got, err)
	}
}

func TestRegisterIndexerTestProviders_PopulatesRegistry(t *testing.T) {
	// Not parallel — mutates the process-global indexers.Default().
	registerIndexerTestProviders(httpclient.NewNopClient())
	for _, k := range []struct{ impl, contract string }{
		{"Newznab", "NewznabSettings"},
		{"Torznab", "TorznabSettings"},
	} {
		if !indexers.Default().Has(k.impl, k.contract) {
			t.Errorf("indexers.Default() missing %s/%s after registration", k.impl, k.contract)
		}
	}
	// Idempotent: a second call must not panic (tolerates ErrAlreadyRegistered).
	registerIndexerTestProviders(httpclient.NewNopClient())
	// nil client is a no-op (no panic).
	registerIndexerTestProviders(nil)
}
