FROM node:24-alpine AS build

WORKDIR /workspace
COPY platform/sdk/typescript ./platform/sdk/typescript
COPY platform/client-ui ./platform/client-ui
COPY platform/hosted-web ./platform/hosted-web

WORKDIR /workspace/platform/sdk/typescript
RUN npm ci && npm run build

WORKDIR /workspace/platform/client-ui
RUN npm ci && npm run build

WORKDIR /workspace/platform/hosted-web
RUN npm ci && npm run build

FROM nginx:1.27-alpine
ENV HOSTED_BACKEND_UPSTREAM=http://backend:8080 \
    NGINX_ENVSUBST_FILTER=HOSTED_BACKEND_UPSTREAM
COPY platform/hosted-web/nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /workspace/platform/hosted-web/dist /usr/share/nginx/html
EXPOSE 8080
