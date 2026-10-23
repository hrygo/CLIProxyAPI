package executor

import "testing"

// TestVertexAPIKeyModelsURL pinpoints the exact URL construction rule for
// API-key mode: the configured base URL is honored as a full resource prefix
// when it already contains "/publishers/", otherwise "/v1/publishers/google/models"
// is inserted for it. Keeps third-party short hosts (zenmux-style) working
// while allowing real Vertex paths that embed projects/locations.
func TestVertexAPIKeyModelsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		model   string
		action  string
		want    string
	}{
		{
			name:    "empty falls back to default Vertex host with /v1",
			baseURL: "",
			model:   "gemini-4-argon",
			action:  "streamGenerateContent",
			want:    "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-4-argon:streamGenerateContent",
		},
		{
			name:    "bare host gets /v1/publishers/google/models appended",
			baseURL: "https://zenmux.ai",
			model:   "gemini-2.5-flash",
			action:  "generateContent",
			want:    "https://zenmux.ai/v1/publishers/google/models/gemini-2.5-flash:generateContent",
		},
		{
			name:    "host with trailing slash still gets /v1/...",
			baseURL: "https://zenmux.ai/",
			model:   "gemini-2.5-flash",
			action:  "streamGenerateContent",
			want:    "https://zenmux.ai/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent",
		},
		{
			name:    "third-party host including /v1 keeps its version and appends suffix",
			baseURL: "https://zenmux.ai/v1",
			model:   "gemini-2.5-flash",
			action:  "streamGenerateContent",
			want:    "https://zenmux.ai/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent",
		},
		{
			name:    "third-party already encoding /publishers/ is used as-is",
			baseURL: "https://api.zenmux.ai/v1/publishers/google/models",
			model:   "gemini-2.5-flash",
			action:  "streamGenerateContent",
			want:    "https://api.zenmux.ai/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent",
		},
		{
			name:    "real Vertex with projects and locations is used as-is (no double /v1)",
			baseURL: "https://aiplatform.googleapis.com/v1/projects/279118250380/locations/us-central1/publishers/google/models",
			model:   "gemini-4-argon",
			action:  "streamGenerateContent",
			want:    "https://aiplatform.googleapis.com/v1/projects/279118250380/locations/us-central1/publishers/google/models/gemini-4-argon:streamGenerateContent",
		},
		{
			name:    "real Vertex with projects and locations uses countTokens action",
			baseURL: "https://aiplatform.googleapis.com/v1/projects/279118250380/locations/us-central1/publishers/google/models",
			model:   "gemini-4-argon",
			action:  "countTokens",
			want:    "https://aiplatform.googleapis.com/v1/projects/279118250380/locations/us-central1/publishers/google/models/gemini-4-argon:countTokens",
		},
		{
			name:    "custom location with regional endpoint preserved verbatim",
			baseURL: "https://us-central1-aiplatform.googleapis.com/v1/projects/279118250380/locations/us-central1/publishers/google/models",
			model:   "gemini-4-argon",
			action:  "streamGenerateContent",
			want:    "https://us-central1-aiplatform.googleapis.com/v1/projects/279118250380/locations/us-central1/publishers/google/models/gemini-4-argon:streamGenerateContent",
		},
		{
			name:    "v1beta1 full prefix is preserved (no forced /v1)",
			baseURL: "https://aiplatform.googleapis.com/v1beta1/projects/279118250380/locations/us-central1/publishers/google/models",
			model:   "gemini-4-argon",
			action:  "streamGenerateContent",
			want:    "https://aiplatform.googleapis.com/v1beta1/projects/279118250380/locations/us-central1/publishers/google/models/gemini-4-argon:streamGenerateContent",
		},
		{
			name:    "whitespace around base URL is trimmed",
			baseURL: "  https://zenmux.ai  ",
			model:   "gemini-2.5-flash",
			action:  "generateContent",
			want:    "https://zenmux.ai/v1/publishers/google/models/gemini-2.5-flash:generateContent",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := vertexAPIKeyModelsURL(tc.baseURL, tc.model, tc.action)
			if got != tc.want {
				t.Errorf("vertexAPIKeyModelsURL(%q, %q, %q)\n  got: %q\n want: %q", tc.baseURL, tc.model, tc.action, got, tc.want)
			}
		})
	}
}
