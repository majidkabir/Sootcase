# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.24 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/sootcase ./cmd/sootcase

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sootcase /sootcase
# The licence and the data sources' terms and credits travel with the image.
COPY LICENSE NOTICE /usr/share/doc/sootcase/
LABEL org.opencontainers.image.source="https://github.com/majidkabir/sootcase" \
      org.opencontainers.image.licenses="PolyForm-Noncommercial-1.0.0" \
      org.opencontainers.image.description="Sootcase freight CO2e calculator. Bundled data keeps its own terms: see /usr/share/doc/sootcase/NOTICE."
EXPOSE 8080
ENTRYPOINT ["/sootcase"]
CMD ["serve"]
