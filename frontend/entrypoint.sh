#!/bin/sh
# API_URL can override the default /api if needed (e.g. external backend)
if [ -n "$ECMS_API_URL" ] && [ "$ECMS_API_URL" != "/api" ]; then
  sed -i "s|const API = window.ECMS_API || '/api';|const API = window.ECMS_API || '${ECMS_API_URL}';|" \
    /usr/share/nginx/html/index.html
fi

exec nginx -g 'daemon off;'
