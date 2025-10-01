JIRA MCP Server using Go
========================

This is a JIRA MCP server written in Go. It is designed to provide a scalable and efficient way to manage and track issues in a JIRA project. The server is built using the Go programming language.

## Pre-Requisites

### Go 1.16 or later
To run this server, you will need to have Go 1.16 or later installed on your system. You can download it from the official Go website.

### JIRA API Key and Username
You will need to have a JIRA API key and username to run this server. You can generate a JIRA API key from your JIRA account settings.

## Running the Server
 Once you have Go installed, you can clone this repository and run the server using the following command:

```
go run main.go
```

This will start the server in stdio mode.

To run the server in SSE mode, you can use the following command:

```
go run main.go --transport sse
```
This will start the server on port 3001 by default. You can change the port using the `--port` flag.

```
