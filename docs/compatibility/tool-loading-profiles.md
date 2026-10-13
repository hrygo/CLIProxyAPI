# Tool loading capability and certification profiles

`CapabilityProfileForRoute` describes an **effective route policy**. It does
not prove that a particular model retrieves a tool, that a client dispatches it,
or that a transport has passed live acceptance. `ClientSearchSupported` keeps
the existing conservative catalog projection: every candidate must permit
native or bridged client search. Temporarily unavailable candidates still
participate. Missing empirical evidence does not automatically switch clients
back to eager declarations.
Candidate enumeration resolves aliases and model pools before transient
availability filtering. Explicitly disabled credentials/models remain excluded;
cooldowns and region errors cannot hide a candidate that may later recover.

The profile reports upstream-owned or cumulative budgeted activation, Lite
declaration position scope, and potential grammar/schema/declaration losses.
Potential losses are conditional on the input; `Prepared.Transformation`
records the actual Lite position changes for one preparation. Bridge-side
position fidelity is unknown because downstream translators also affect scope.
HTTP buffered/SSE values of `declared` mean policy support for client search,
not a live certificate. WebSocket replay and duplex remain `unknown` until
their independent protocol/transport validation is complete.

Policy profiles always carry `evidence: unknown`. Empirical records are separate
`CertificationRecord` objects, bound to gateway revision, policy generation,
client/version/build/effective mode, source protocol, provider/model/format, opaque endpoint ID, transport,
catalog/fixture/permission hashes, and region ID. A missing dimension is the
literal string `unknown`, never a fabricated version, zero, or null.
`StateForScope` marks changed scope `revalidation-required`; unknown dimensions
cannot promote a scoped record. A single provider region error is an availability
observation and does not change technical route support.

`StateForScope` checks applicability, not evidence authenticity. Any evidence
consumer must inspect the artifact and verify its digest and assertions before
creating a positive record. A manifest that validates structurally is not proof
of passed acceptance, task quality, or savings. Keep failed and incomplete cases
in the artifact; certification labels cannot remove them from denominators.

The earlier artifact schema and acceptance tooling are retained as an
[experimental snapshot](https://github.com/hrygo/CLIProxyAPI/tree/d9e64b8eff81414244703220d44d651d195ee428/ops/tool-loading).
They are excluded from the production candidate and are not a prerequisite for
gateway fixes, CI, or route support. The runtime profiles and scoped record
types remain internal diagnostics; they do not certify evidence authenticity.
Positive scoped records still require known scope and a SHA-256 artifact digest.

Offline validation:

```sh
GOTOOLCHAIN=go1.26.4 go test ./internal/responsestools ./sdk/cliproxy/auth -run 'Test(CapabilityProfile|CertificationScope|ClientSearchSupported)' -count=1
```

These profiles are internal diagnostics and evidence artifacts. This change
adds no management endpoint, default policy switch, or certification-driven
routing behavior. Live verification is scoped to concrete production changes;
unknown transports and provider behavior remain unknown until directly verified.
