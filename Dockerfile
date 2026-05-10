FROM gcr.io/distroless/static-debian12:nonroot
COPY astrotui /usr/local/bin/astrotui
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/astrotui"]
