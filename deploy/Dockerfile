# Image von Monika (docs/OPERATIONS.md, Auslieferung). Das Binary baut make lokal; Go wird nie in
# Docker gebaut. Bauen mit: make image
FROM gcr.io/distroless/static-debian12:nonroot

ARG BINARY=bin/linux-amd64/monika
COPY ${BINARY} /monika

USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s CMD ["/monika", "-healthcheck"]
ENTRYPOINT ["/monika"]
