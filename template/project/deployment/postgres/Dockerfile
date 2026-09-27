FROM alpine:3.21

RUN apk add --no-cache curl postgresql-client jq

COPY init-by-labels.sh /init.sh
RUN chmod +x /init.sh

ENTRYPOINT ["/bin/sh", "/init.sh"]
