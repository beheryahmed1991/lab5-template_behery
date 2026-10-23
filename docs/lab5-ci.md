# Lab 5: automated instructor tests

The GitHub Classroom Workflow runs on pushes to `master` and manual dispatch.
It builds, unit-tests and vets the four Go services. The API job creates a
disposable Kubernetes cluster with kind, builds and loads Docker images, and
deploys the existing PostgreSQL and service Helm charts. Gateway is forwarded
to `http://127.0.0.1:8080` on the CI runner. This does not use your local Minikube,
require publishing Docker images, or need a paid cluster.

## Repository secrets

In GitHub open **Settings > Secrets and variables > Actions > New repository secret**:

| Name | Value |
| --- | --- |
| `AUTH0_CLIENT_ID` | Client ID of the `hotels` application |
| `AUTH0_CLIENT_SECRET` | Current Client Secret of that application |
| `AUTH0_TEST_USERNAME` | Email of a dedicated Auth0 database test user |
| `AUTH0_TEST_PASSWORD` | Current password of that user |

The public issuer, audience and JWKS URL are specified in the workflow. The API
Identifier must be `https://lab5-api`; grant `hotels` user-delegated access to it.
Enable Password grant and the database connection for the application. Set the
tenant Default Directory to that connection. Use credentials verified in Postman,
after rotating any exposed in screenshots. Outbound access to Auth0 is necessary.

## Instructor collection

`tools/postman/run.cjs` reads `v2/postman/collection.json` and `environment.json`
without modifying them. It supplies the environment in memory. Only in the runtime
copy of the token request, it renames `clientId`/`clientSecret` to Auth0's
`client_id`/`client_secret` and adds the audience. Instructor assertions, request
order, API routes and booking data remain unchanged. Credentials and tokens are
not exported. The reporter prints names, HTTP statuses and assertion results,
without printing request/response bodies or Authorization headers.

Tests create and cancel a reservation in the disposable CI database, not your
local database. The optional Go PostgreSQL integration test requiring
`LOYALTY_TEST_DATABASE_URL` is skipped in the unit-test jobs; the instructor API
job separately exercises the real services with PostgreSQL.

## Results and limitations

After adding secrets, push and open the **new** Actions run. Re-running an old run
uses the older commit. Missing secrets fail before cluster creation. Failing
requests or assertions produce a nonzero exit status; failures are not ignored.
Diagnostics print pod states, events and service logs. The disposable cluster is
deleted at the end, including its test data.

The Newman result determines the Actions result. `autograding.json` points to the
same runner for environments that invoke Classroom grading after services and
environment variables have been configured. Legacy Classroom and Google Sheets
marker actions are not executed: this manually created repository has no confirmed
Classroom connection or teacher-owned Sheets credentials. A green run does not
register a Classroom grade automatically.

CI uses Gateway port-forward; it does not validate Ingress or managed cloud
deployment. Those acceptance requirements, if applicable, need separate validation.
Local syntax and configuration checks do not prove a successful GitHub run with
real Auth0 credentials; that result must be confirmed after pushing.
