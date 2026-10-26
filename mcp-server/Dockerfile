FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mcp-server .

FROM scratch
COPY --from=build /out/mcp-server /mcp-server
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/mcp-server"]
