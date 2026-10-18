# PERF-01 — BitrixCluster control-plane scale

The disposable kind experiment loads **10, 100 and 1000 BitrixCluster CRs**,
one size at a time in a new namespace. Each resource uses the real installation
sample but intentionally lacks dependency Secrets. The test measures controller
queue, informer/API and status convergence, **not** the Bitrix web workloads,
storage, database, Redis, Gateway or live traffic.

For each size the test creates CRs, waits for the current generation with
Ready=False / SecretMissing, updates the release tag to bump spec generation,
then waits for the new generation. The JSON report records completed / pending
objects, elapsed time and p50/p95/p99 **upper bounds** of observed status times,
including all API writes and one-second polling intervals. These are not
per-reconcile duration measurements.

Run GitHub Actions / Performance / Run workflow on the target commit to create
a fresh kind, build/deploy manager, execute all three sizes, and publish an
artifact named bitrix-perf-report containing perf-results.json.

On an already-prepared disposable kind use:

    python3 test/performance/reconcile_load.py --context kind-test \
      --sizes 10 100 1000 --timeout 300 --poll 1 --output perf-results.json

Unit tests (no kubeconfig):

    python3 -m unittest discover -s test/performance -v

The runner refuses any kube context not starting with kind-. A separate run
namespace is created for each size and deleted even on failure. Do not run on
a kind cluster with valuable workloads: context prefix alone is not proof of
disposability. Per-size convergence timeout is 300 seconds; nonconvergence is
a failure. For fair comparison use identical API-server/node image, manager
resource limits (the workflow uses 2 CPU / 2 GiB), and otherwise idle runners.

Check workqueue depth, reconcile time, status latency and controller CPU/RSS
as complementary signals. Results for 10/100/1000 **have not been measured**
until the manual workflow executes; avoid reporting imaginary throughput.
