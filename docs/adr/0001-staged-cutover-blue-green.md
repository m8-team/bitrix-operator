# ADR-0001: Blue-Green staged cutover for BitrixCluster Gateway publication

Status: **Proposed / design only** (2026-10-30). No runtime, API or CRD implementation in this PR.

## Decision summary

Implement an opt-in `Staged` strategy using **two independently selectable,
immutable-revision web Deployments + Services** and **one authoritative,
stable public HTTPRoute per hostname**. Prepare and verify the candidate before
moving public traffic. The public HTTPRoute keeps its object identity and only
its `backendRefs` are changed (optionally by canary weights); keep the previous
Deployment/Service running for a bounded automatic-rollback window. Isolate
pre-cutover L7 probes on a **private Gateway/listener** and verify the actual
backend revision, not merely HTTP 200. After promotion probe the **public**
data path and evaluate error-rate/latency guardrails. Never assert mathematical
zero downtime: Gateway API config propagation to different data-plane instances
is asynchronous.

The default strategy remains `Rolling` with existing behavior; no implicit
upgrade of existing clusters.

## Current-state findings (from main)

- `internal/workload/web.go`: one named Deployment `<cluster>-web`, one Service
  `<cluster>-web`, both selecting the broad cluster owner labels. There is no
  version discriminator in the Service selector.
- `internal/workload/gateway.go`: every generated HTTPRoute forwards to the
  same `<cluster>-web:80` Service; a different HTTPRoute name does **not**
  select a different release.
- `internal/controller/reconcile.go`: web runtime reconciliation executes
  *before* `reconcileGateway`, so preserving an old Route does not pin the
  application behind the shared Service.
- `internal/workload/web.go` uses `Recreate` for uploads PVC without RWX and
  `RollingUpdate` only when `ReadWriteMany` is requested. Shared writable
  uploads, sessions and DB must be addressed before parallel versions.
- `internal/controller/sites.go` already has image-digest and site-validation
  proof concepts. Reuse them, but bind each proof to its own revision/Pod set.
- PRs #72–#75 already add route readiness, retention and real Envoy HTTPS
  end-state and traffic-sampling tests. They are **not** a versioned backend
  handoff mechanism.

## Core invariants

1. **Single public authority:** for a hostname/path there is exactly one
   operator-owned authoritative public HTTPRoute. Do not create an equal
   precedence replacement public Route: older Routes win ties under Gateway
   API match precedence.
2. **Revision isolation:** a Service selects Pods of exactly one immutable
   revision; selectors of different revisions cannot overlap; readiness,
   Endpoints and probes are revision-scoped. Never repoint the old Service's
   selector to a new Pod revision.
3. **Serving continuity:** old web Pods/Service, immutable runtime ConfigMaps
   and referenced credentials remain valid until the new data path is verified,
   rollback is no longer required and connections have drained.
4. **Unchanged public traffic before promotion:** candidate receives no public
   traffic during prepare/probe. An isolated private preview Gateway is
   mandatory for strict pre-cutover Gateway probing; a public header/nonce
   route alone is **not** an access control boundary.
5. **Proof of the intended target:** every probe result is tied to the desired
   BitrixCluster UID, generation, revision ID, image digests, config hash,
   runtime Secret identities, backend Service UID and probe attempt UID.
   HTTP 200, `Accepted=True` and `Programmed=True` alone do not prove this.
6. **Safety over availability:** disable-publication, revoked ReferenceGrant,
   invalid TLS, deleted listener, lost ownership or policy violation retains
   existing explicit withdrawal semantics; do not keep an unsafe listener open
   in the name of zero downtime.
7. **No cross-tenant adoption or accidental GC:** all changes are UID-owner
   validated, SSA field-managed, update preconditioned and replay-safe.

## Proposed topology

~~~text
                          [PUBLIC Gateway listener]
User ──────────────────>  stable owned HTTPRoute "portal"  (existing UID)
                            backendRefs:
                              active-svc (100 -> 0)
                              candidate-svc (0 -> 100)
                                 |               |
                   +-------------+               +------------------+
                   v                                                v
       web-svc-r<active>                                  web-svc-r<candidate>
       selector revision=A                                selector revision=B
             |                                                   |
       Deployment web-rA                                    Deployment web-rB
       immutable config/image                               immutable config/image
       old code, N ready replicas                           new code, N ready replicas
             |                                                   |
             +------------ compatible DB/Redis/uploads ---------+

[probe Job, restricted namespace/network]
    -> [PRIVATE Gateway/listener, TLS] -> [preview HTTPRoute] -> candidate-svc
    -> verify authenticated revision identity and application health
~~~

Resource names are bounded DNS names derived from Cluster UID and truncated
revision hash, not from untrusted arbitrary hostnames. Example:
`<cluster>-web-r7f3a2d91`, `<cluster>-web-svc-r7f3a2d91`. Keep owner
references, `bitrix.m8.team/owner-uid`, and add
`bitrix.m8.team/web-revision=<hash>` plus a role label. Both Deployments,
Services, Pod selectors, HPA targets, NetworkPolicies, PDBs and availability
observations must be version-scoped; in particular do not reuse current broad
`labels(c)` as a Deployment/Service selector for both revisions.

### Identity: immutable intent and observed runtime

- `revisionID` = truncated SHA-256 over canonical **non-secret** effective web
  desired state: release image **digest**, rendered nginx/PHP configuration
  hash, PHP/nginx image digests, site-host mapping, gateway listener-derived
  runtime settings, and referenced Secret UID/resourceVersions. A tag alone
  does not prove immutability. Use full hash in status/annotations.
- Before runtime digests are resolved, use a provisional `intentHash` for
  candidate creation and reconcile the final digest-bound revision identity.
  Do not silently mutate an already adopted immutable revision.
- Record the active revision, candidate revision, pinned original public route
  UID/name, currently-applied backend spec hash, expected route generation and
  rollout attempt. Status is not a secret store. Published route identity must
  not depend on mutable site ID; bind each stable hostname to one route object.
- ConfigMaps are already immutable; protect *runtime credentials* separately:
  a mutable Secret reference can change in both old and new Pods at once.
  Strict staged mode requires externally versioned, stable Secret references or
  a documented compatibility gate. The operator must not start copying Secret
  values into status or create new credential Secrets implicitly.

### Stable public route and backward-compatible migration

For new staged clusters, create stable per-hostname HTTPRoutes (hash of canonical
hostname + cluster UID) with a single default "/" match. Avoid chunking several
hosts with independently different rollout states into one rule. Keep the same
HTTPRoute UID across revisions. For existing clusters that have site-ID-derived
Route names, **adopt the existing owned public Route as the traffic anchor**
and patch only `backendRefs` to its versioned *active* Service after that
Service has been checked. Persist its name/UID per hostname; do not delete the
old Route to obtain the new naming scheme during migration. A later explicit
migration may rename it under independent traffic safeguards.

The public Route has the active backend at 100 and candidate at 0 until
promotion. A strict first implementation may omit a zero-weight backend
rather than rely on implementation-specific endpoint behavior at weight zero.
Promotion uses an optimistic update on the **same authoritative HTTPRoute**
to replace the one backend, or adjust the backend weights. This is an atomic
API-object specification change, **not** an atomic multi-Envoy data-plane
switch. Wait for current Route parent `Accepted/ResolvedRefs`, Gateway and
listener `Accepted/Programmed` and any implementation-specific XDS ACK when
available; then verify actual public HTTPS responses from the new revision.

Do not use a second public Route with an identical host/path as the cutover
mechanism: match precedence would continue to prefer the older Route.

## Pre-cutover verification and trust boundary

The candidate must have ready Pods, matching owners/ReplicaSets/digests,
EndpointSlices with sufficient ready addresses, matching ports, healthy DB
connections, consistent Redis sessions, actual site validation and no stale
config/Secret identity. Require valid Gateway/listener/TLS/ReferenceGrant
conditions for the **private** probe route.

Perform HTTP(S) probes through an infrastructure-provided private Gateway
listener, *not* through the manager's unrestricted HTTP client and not by
creating a public header-only bypass. NetworkPolicies must restrict the
private preview listener and probe Job to the intended namespace, listener,
candidate service and minimal DNS. A probe Job uses non-root, read-only rootfs,
no API token, bounded resources and deadline, pinned image, strict certificate
verification, no redirects, and no arbitrary user-supplied destination
addresses. It sends the original expected Host/SNI only to the explicitly
allowlisted Gateway address. No link-local/metadata endpoint access.

Two independent evidence classes are required:

- **Infrastructure evidence:** the pinned private HTTPRoute is accepted and
  resolves only the candidate Service, and the response contains a trusted
  revision marker generated by the candidate workload (not merely an arbitrary
  Gateway-added header). Markers should identify revision + release/config
  identity and are validated against the bounded probe attempt.
- **Application evidence:** an operator-defined or user-provided safe,
  read-only PHP application check confirms the intended site and required
  dependencies. Do not silently run application-changing requests. Optional
  smoke paths are explicit allowlists; no redirects and no auth credentials
  logged to status or Events.

The preview Gateway can validate most Envoy routing/TLS behavior, but cannot
prove that the *public* listener has received the new config. A separate
post-promotion public probe + SLO monitor is mandatory.

If the required private Gateway and isolated probe runtime are not available,
`Staged` in strict mode is **Blocked/PreviewUnavailable**; it does not silently
use public header-matching or claim zero downtime. A less strict future mode
may validate only the candidate Service, but must expose the reduced guarantee.

### PVC, database, sessions, caches and background writers

Uploads are shared mutable application state. Strict parallel old/new rollout
requires storage able to mount both web revisions safely and coherently (for
example a validated RWX filesystem or external object-storage integration).
**RWO is node-scoped, not a portable multi-node dual-version guarantee**;
`ReadWriteOncePod` forbids two concurrent Pod writers. Do not silently
co-schedule both Deployments on one node or clone a stale uploads snapshot and
call it equivalent. Block staged mode with `StorageIncompatible` until a safe
backend is configured. Never GC the retained uploads PVC.

DB migrations must be expand/contract and compatible with both revisions for
the entire rollback window. Destructive DDL requires an explicit maintenance
path outside the zero-downtime promise. Sessions and caches need compatible
shared Redis schema/serialization or versioned namespaces. Cron, agents and
Push publishers remain **single active writers**; candidate web can serve
read-only validation but must not create a duplicate cron/agent execution.
Both active/candidate may coexist only under a validated resource and
compatibility budget.

### Proposed opt-in API (not yet implemented)

~~~yaml
spec:
  runtime:
    web:
      rollout:
        strategy: Staged       # default remains Rolling
        promotion: Automatic   # or Manual, with an explicit approval token
        preview:
          gatewayRef:           # infrastructure-owned PRIVATE listener
            namespace: private-gateway
            name: preview
            sectionName: https
          probePath: /__m8/verify
          maxAge: 5m
          minSuccesses: 5
          timeout: 3s
        traffic:
          # Strict blue/green default: [100]; optional canary after readiness
          steps: [100]          # alternative [1, 5, 25, 50, 100]
          minStepDuration: 2m
          maxErrorRate: 0.01
          maxP95LatencyMs: 750
          minimumSamples: 100
        rollback:
          enabled: true
          observationWindow: 15m
          drainSeconds: 120
status:
  rollout:
    attemptID: "<uuid>"
    phase: "PreviewReady"
    activeRevision: "<full sha256>"
    candidateRevision: "<full sha256>"
    routeBindings:
      - hostname: portal.example.com
        name: mysql-route-legacy-id  # persisted UID of public anchor
        uid: "<uid>"
        expectedGeneration: 5
    weightCandidate: 0
    lastEvidenceAt: "<timestamp>"
    conditions:
      - type: PreviewReady
        status: "True"
      - type: PromotionReady
        status: "False"
        reason: AwaitingApproval
~~~

This is a **contract sketch**, not a drop-in CRD patch. Define precise CEL
validation, defaults, max lengths/durations, enums, upgrade behavior and status
deep-copy generation in implementation. Keep site URLs consistent with
*actually serving* revision. Expose `ServingReady` (active old still healthy)
separately from `Ready`/`DesiredRevisionReady` (new desired version reached
public readiness). Never report desired generation Ready based on the old Pods.

## State machine, preconditions and recovery

| Phase | Entry/transition guard | Stable user path / next action |
| --- | --- | --- |
| `Preflight` | Pin old route UID/backend and active revision; validate PVC, external secrets, DB/session compatibility, Gateway capability, tenant rights and capacity | 100% old; block unsupported situations |
| `Preparing` | Create revision-scoped config, Deployment, Service, PDB/HPA policy and EndpointSlice watches | 100% old; retry without altering live Service |
| `RuntimeReady` | Ready/available candidate Pods, EndpointSlices, owner chain and digests match; site validation proof current | 100% old |
| `PreviewReady` | Private Gateway/HTTPRoute accepted/programmed; bounded probe Job returns exact revision and application proof | 100% old; snapshot proof identity/time |
| `Promoting` | Admission policy allows promotion; route UID/RV and old backend hash verified; promotion token/attempt current | Patch single public route to candidate weight; no duplicate equal-precedence Route |
| `Observing` | Route conditions current + public endpoint identifies candidate + min requests/duration + SLO guardrails | Retain old Pods, routes and config through observation window |
| `Completed` | 100% candidate stable for observation and drain windows; no old in-flight traffic according to available signals | Pin new active status; GC old owned revisions safely |
| `RollingBack` | Failed promotion gate, SLO breach, bad public revision or user abort | Repoint the **same** public route to old Service; verify and keep candidate for diagnosis until bounded cleanup |
| `Blocked` | Unsafe/missing credentials, PVC capability, private preview, tenant isolation, ownership collision or incompatible DB schema | Do **not** mutate public route; keep old healthy; surface actionable condition |

The live public Route's backendRef is the authoritative serving pointer after
a manager restart; **never** infer which version serves traffic only from the
cached `status.rollout.phase`. Reconcile from APIReader, compare UID,
generation, backend hash and owned Service/Deployment revision before any
transition; resume, roll back or block idempotently. Serialize per-cluster
rollout attempts with `attemptID` and optimistic resourceVersion writes;
discard old probe results if spec generation/Secret identity changes. On a
new desired generation: discard unpromoted candidate; if traffic was already
shifted, first restore/confirm a serving stable version before preparing
another candidate. Unknown partial cutover uses fail-safe observed-route
recovery, not blind deletion. GC with owner UID/RV preconditions only.

Manual approval must be bound to current revision/attemptID and expire; a
boolean permission-less annotation is not a safe approval protocol. Keep
status updates through existing `ComponentResult`/`StatusManager` only.

### Traffic shifting and rollbacks

The default first implementation uses 100% old → 100% new after private
preview verification; optional weighted steps can be enabled after a separate
canary conformance test. Gateway API HTTPRoute `backendRefs.weight` supports
weighted distribution, but weights are proportional, not guaranteed per-user
percentages. Canary can split one session across versions; require compatible
session state or sticky-routing policy when explicitly supported and tested.
A minimum sample threshold must not silently allow promotion on low traffic.
Telemetry sources must distinguish active versus candidate revision; user
traffic with errors/latency above configured bounds aborts and triggers
rollback. Request mirroring is **off by default** because duplicated Bitrix
write requests can produce side effects; it cannot prove candidate responses
either.

Rollback is forward-only for database migrations; the web traffic pointer
reverts, not the database schema or user uploads. Keep old revision running
during the observation window and block GC if rollback has not been verified.
After rollback, surface `ServingReady=True` but `DesiredRevisionReady=False`
with failure evidence and bounded retry/backoff.

## Minimal implementation plan (in order)

1. **v0: capability & safety gates**: typed rollout API/status, explicit
   `Rolling` default, storage/RWX check, external secret revision policy,
   namespace and capacity validation; status/condition tests. No traffic move.
2. **v1: revision-scoped workloads** in `internal/workload`: label selectors,
   versioned Deployments/Services/ConfigMaps, candidate readiness/
   EndpointSlices, SiteValidation by revision, active HPA handling, single
   cron/Push owners; backwards-compatible first migration from `-web`.
3. **v2: private preview**: provisioned preview Gateway reference (not
   auto-creating public infra), isolated HTTPRoute, per-attempt bounded
   probe Job, identity verification, replay/failure/SSRF tests.
4. **v3: stable public binding + switch/rollback**: keep existing HTTPRoute
   UID for migration; route/backend optimistic updates; state machine
   transitions and crash/reconcile recovery; `ServingReady` separate from
   desired-version status; observation window and delayed GC.
5. **v4: canary / SLO guardrails (optional)**: weights, revision-labelled
   metrics, minimum samples, rollback thresholds, sticky-session capability
   and controller-specific conformance tests. Strict mode fails closed on
   unsupported capabilities.

Recommended new modules: `internal/controller/rollout.go` (state machine),
`rollout_state.go` (status/attempts), `rollout_probe.go` (bounded Job evidence),
`internal/workload/revision.go` (immutable workloads),
`internal/workload/route_binding.go` (single public binding + private probe
route), and targeted `rollout_*_test.go`. Preserve `ResourceManager` for SSA,
`StatusManager` for status changes, and keep rollout as explicit
`ReconcileStep` stages rather than adding opaque logic to `gateway.go`.
Required RBAC deltas must be justified narrowly (e.g. read-only
`discovery.k8s.io/endpointslices` and creating an owned, restricted probe Job
with no extra Secret write permissions).

## Test and release acceptance matrix

- Envtest: immutable revision selectors, owner collisions, UID/RV CAS,
  cached/stale `observedGeneration`, old/new Service isolation, 0/100
  weights, preview availability, API outage and cleanup before/after
  promotion.
- Real kind + Envoy: old still serves during candidate Pod rollout;
  preview checks *candidate* via private listener; 100% switch; public
  revision marker and HTTPS success; reversible switch; kill manager
  between `Promoting`/`Observing`/`RollingBack`; fail a candidate Pod
  and Gateway xDS config mid-rollout.
- Continuous HTTPS sample across switch and rollback, with nonzero
  traffic volume, error-rate and observed revision per response.
  Sampled success is not proof that zero requests failed.
- Storage tests: real RWX multi-node, refused RWO/RWOP, no upload
  corruption and no PVC deletion; DB/session compatibility, release
  digest and Secret rotation; no duplicated cron, Push or agents.
- Security: network deny-all except bounded preview egress, TLS pinning/
  CA trust, no localhost/metadata SSRF, private listener not publicly
  routable, no arbitrary HTTP headers that unlock public candidate
  access, no Secret content in probes/status/logs.
- Chaos: lost controller leader, API timeout, stale route status, late
  status after new attempt, foreign Route takeover, manual disable and
  broken Gateway grant/TLS; always prefer correct ownership/safety
  over availability.
- Gate: non-staged mode is byte-for-byte behaviorally compatible;
  new staged rollout is feature-gated and initially approved only for
  validated RWX+Envoy environments, not generic Kubernetes clusters.

## Explicitly out of scope / known limits

- An absolutely atomic change across all distributed Envoy replicas is
  not guaranteed by one Kubernetes HTTPRoute update; control-plane
  status is not a traffic availability promise.
- Arbitrary production traffic mirroring, mutation of DB schema,
  backup/restore of uploads, cross-region DNS/global-load-balancer
  switching, automatic issuance of TLS credentials and cross-Gateway
  traffic migration are separate projects.
- A public header-matched preview route is not sufficiently private
  even if it contains an opaque nonce. It is **not** accepted as the
  standard strict staged cutover mechanism.
- A rollback cannot recover from breaking DB migrations, externally
  revoked credentials or changes to shared user data; these are
  explicit preflight blockers or operational incident paths.

## Standards and further reading

- Gateway API HTTPRoute match precedence and semantics:
  https://gateway-api.sigs.k8s.io/reference/api-types/httproute/
- Gateway API traffic splitting/canary:
  https://gateway-api.sigs.k8s.io/guides/user-guides/traffic-splitting/
- Gateway API Implementer's Guide (`Programmed` is not traffic proof):
  https://gateway-api.sigs.k8s.io/guides/implementers-guide/
- Kubernetes PV access modes and caveats:
  https://kubernetes.io/docs/concepts/storage/persistent-volumes/
- Kubernetes Deployment rollout and selector non-overlap:
  https://kubernetes.io/docs/concepts/workloads/controllers/deployment/
