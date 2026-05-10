FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/alphabravo-oss/astronomer-tui"
LABEL org.opencontainers.image.description="Astronomer TUI Kubernetes terminal UI"
LABEL org.opencontainers.image.licenses="MIT"
COPY astrotui /usr/local/bin/astrotui
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/astrotui"]
