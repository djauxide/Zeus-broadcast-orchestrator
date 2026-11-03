# Zeus kernel image. The Go binaries are cross-compiled for linux/amd64
# (see Makefile target `image-linux`); this Dockerfile only packages them.
# distroless/static provides CA certificates (required for the Kubernetes
# API TLS client) and a nonroot user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY bin/linux/manager /manager
COPY bin/linux/zeus-nap-validate /zeus-nap-validate
USER nonroot:nonroot
EXPOSE 8080 8081 9443
ENTRYPOINT ["/manager"]
