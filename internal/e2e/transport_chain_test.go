package e2e_test

import "testing"

// TestChainOverH2C reruns the real BFF → real tripmap → isolated PostgreSQL/Redis
// chain with h2c on the internal hop: per-user isolation, anonymous denial, the
// message limit, deadlines, cancellation and the no-retry rule must not depend on
// the transport. Like every chain test it skips without the TRIPMAP_TEST_* stores.
func TestChainOverH2C(t *testing.T) {
	for name, run := range map[string]func(*testing.T){
		"footprint sync and isolation": TestChainFootprintSyncAndIsolation,
		"anonymous denied":             TestChainAnonymousFootprintDenied,
		"oversized message":            TestChainGrpcOversizedMessageRejected,
		"deadline budgets":             TestChainDeadlineBudgets,
		"client cancellation":          TestChainClientCancellationPropagates,
		"no automatic retry":           TestChainNoAutomaticRetryOnUnavailable,
		"forged client ip":             TestChainForgedClientIPIgnored,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TRIPMAP_TEST_GRPC_TRANSPORT", "h2c")
			run(t)
		})
	}
}
