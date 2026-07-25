# Observation adapters

## Data classification and minimization

HTTP and browser adapters assign `observation.Sensitivity` from trusted adapter
options; sensitivity is never accepted from an event body or page. Their
production default is `confidential`. The recorder defaults other captured
events to `internal`.

Use a deterministic ingress redactor for fields that should never enter a
trace:

```go
redactor, _ := observation.NewRedactor(observation.RedactorOptions{
    Rules: map[string]observation.RedactionAction{
        "input.password":               observation.RedactDrop,
        "input.payment.card_number":    observation.RedactMask,
        "output.credentials.*":         observation.RedactDrop,
        "initial_context.access_token": observation.RedactDrop,
    },
})
recorder, _ := observation.NewRecorderWithOptions(
    observation.RecorderOptions{Store: store, Sanitizer: redactor},
)
```

Rules are application policy. Do not derive them from page content, documents,
or model output.

Observation adapters translate source-specific activity into semantic
`observation.Event` values. The core contract deliberately does not prescribe
whether an adapter owns an HTTP server, browser extension, desktop process,
database listener, or CLI wrapper:

```go
type Adapter interface {
    Metadata() AdapterMetadata
}

type Sink interface {
    Capture(context.Context, string, Event) (Event, error)
}
```

`observation.Recorder` implements `Sink`. Adapters own their transport
lifecycle and emit events through that boundary. Stores atomically validate
event ordering, correction references, and duplicate IDs.

## HTTP business events

`observation/httpadapter` is a concrete `http.Handler` for authenticated
business-application events. It:

- accepts only `POST application/json`;
- strictly rejects unknown JSON fields;
- defaults to a 1 MiB request limit and permits at most 16 MiB;
- obtains tenant and actor identity from authenticated headers, never JSON;
- fixes source to `api`;
- fixes trust from handler configuration, never JSON;
- rejects stale signatures;
- rejects duplicate event IDs atomically;
- returns bounded error codes without echoing payloads or secrets.

Every producer must supply a stable `event_id`. Retrying the same signed event
then produces one capture and subsequent `409 event_conflict` responses.

### HMAC authentication

The supplied authenticator uses:

```text
X-Skawld-Tenant-ID
X-Skawld-Actor-ID
X-Skawld-Timestamp
X-Skawld-Signature
```

The timestamp is RFC3339Nano. The hexadecimal HMAC-SHA256 signature covers the
exact bytes:

```text
timestamp + "\n" + tenant_id + "\n" + actor_id + "\n" + request_body
```

Use `httpadapter.Signature` in Go producers. `SecretResolver` receives both
tenant and actor IDs, allowing an application to retrieve identity-specific
keys from its existing secret manager. `NewStaticSecrets` is suitable for
tests and local examples.

HMAC does not replace TLS. Production endpoints must use HTTPS, rotate tenant
keys, apply request-rate limits at the ingress, and restrict network access as
appropriate.

### Trust and prompt injection

Authentication proves which integration delivered an event; it does not make
arbitrary content inside that event a trusted instruction.

Configure one handler per trust boundary:

```go
trustedERP, _ := httpadapter.New(httpadapter.Options{
    Sink: recorder,
    Authenticator: authenticator,
    Trust: observation.TrustApplicationEvent,
})

supportTickets, _ := httpadapter.New(httpadapter.Options{
    Sink: recorder,
    Authenticator: authenticator,
    Trust: observation.TrustUntrustedContent,
})
```

Only `application_event` and `untrusted_content` are accepted by this adapter.
HTTP payloads cannot claim `system_policy` or `human_instruction`. Downstream
extractors must keep untrusted content separate from instructions and must
never turn content text directly into executable tool calls.

Run the local credential-free example:

```sh
go run ./examples/http_observation
```

## Browser semantic events

`observation/browseradapter` accepts instrumentation events from a browser
extension, accessibility bridge, or embedded application and converts them to
`SourceBrowser` observations. It records semantic operations such as
`navigate`, `activate`, `input`, `select`, `submit`, `extract`, `download`, and
`upload`.

Targets use accessibility/application identity:

```go
browseradapter.Element{
    Role:     "button",
    Name:     "Submit payment",
    StableID: "payment.submit",
}
```

The adapter deliberately has no coordinate, CSS selector, XPath, or script
fields. It recursively rejects those replay primitives if a producer hides
them in input, output, context, or result maps. Page identity is split into a
validated HTTP(S) origin and a bounded path. Events have a configurable byte
limit, require the authenticated tenant and actor from context, and receive a
fixed adapter trust level that payloads cannot override.

Browser authentication and transport are deployment concerns. A browser
extension should authenticate to an application-owned ingress, which verifies
the identity and then invokes the adapter with `core.WithPrincipal`. DOM text,
page content, downloaded files, and form values normally remain
`untrusted_content`; authentication does not convert them into instructions.
