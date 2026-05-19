FROM nginx:alpine

RUN apk --no-cache add gettext

RUN mkdir -p /certs
COPY certs/frontend.crt /certs/frontend.crt
COPY certs/frontend.key /certs/frontend.key
COPY certs/ca.crt       /certs/ca.crt

RUN cp /certs/ca.crt /usr/local/share/ca-certificates/ecms-ca.crt && \
    update-ca-certificates

COPY frontend/index.html    /usr/share/nginx/html/index.html
COPY frontend/nginx.conf    /etc/nginx/conf.d/default.conf
COPY frontend/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 80 443
ENTRYPOINT ["/entrypoint.sh"]
