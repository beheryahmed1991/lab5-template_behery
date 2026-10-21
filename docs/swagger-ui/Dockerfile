FROM swaggerapi/swagger-ui:v5.33.2

COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY openapi.yaml /usr/share/nginx/html/openapi.yaml

ENV SWAGGER_JSON_URL=/openapi.yaml \
    VALIDATOR_URL=none \
    DOC_EXPANSION=list \
    DISPLAY_REQUEST_DURATION=true
