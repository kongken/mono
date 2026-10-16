package application

// EndpointHashForTest exposes the push endpoint digest to external tests.
func EndpointHashForTest(endpoint string) string { return endpointHash(endpoint) }
