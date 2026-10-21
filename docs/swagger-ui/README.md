# Swagger UI

Run from the repository root with Docker Desktop running:

```powershell
docker compose --profile docs up -d --build
```

Open http://localhost:8081, expand an operation, choose **Try it out**, enter
the parameters, and choose **Execute**. Responses show the HTTP status and JSON.

For authenticated operations use `X-User-Name: Test Max`. To create a booking,
use the seeded hotel UID `049161bb-badd-4fa8-9d90-87c9a82b0668` and an end date
after the start date. Copy the returned `reservationUid` to view or cancel it.

The UI forwards API requests to Gateway inside Docker. The OpenAPI server URL
is `/`, so the browser uses the same origin as the UI. Gateway remains available
directly on http://localhost:8080.

`openapi.yaml` starts from the instructor's v2 specification. It has a relative
server URL, example user headers, default pagination, and the Gateway health
endpoint. Keep its schemas and responses in sync when API behavior changes.

Swagger UI is enabled with the `docs` Compose profile. The instructor's API
tests continue using port 8080 and do not require the UI container.

To stop the UI only:

```powershell
docker compose --profile docs stop swagger-ui
```

To run the instructor's fault tolerance scenario once those mechanisms are
implemented, use Git Bash or WSL from the repository root:

```bash
bash scripts/test-script.sh v2 loyalty-service 8050
```

The scenario stops and starts Loyalty Service and checks the resulting behavior.
Swagger UI can be used for manual demonstrations; it does not replace these tests.
