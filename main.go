package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName    = "jira-mcp-server"
	ServerVersion = "1.0.0"
)

type JiraMCPServer struct {
	server     *mcp.Server
	config     *JiraConfig
	jiraClient *jira.Client
}

type JiraConfig struct {
	BaseURL    string
	Username   string
	APIToken   string
	ProjectKey string
}

type CreateJiraIssueParams struct {
	Summary      string                 `json:"summary"`
	Description  string                 `json:"description"`
	IssueType    string                 `json:"issueType"`
	Priority     string                 `json:"priority"`
	ProjectKey   string                 `json:"projectKey,omitempty"`
	Labels       []string               `json:"labels,omitempty"`
	Components   []string               `json:"components,omitempty"`
	CustomFields map[string]interface{} `json:"customFields,omitempty"`
	Assignee     *jira.User             `json:"assignee,omitempty"`
}

type UpdateIssueArgs struct {
	IssueKey    string `json:"issueKey"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
}

func (j *JiraMCPServer) UpdateJiraIssue(ctx context.Context, req *mcp.CallToolRequest, params *UpdateIssueArgs) (*mcp.CallToolResult, any, error) {

	issue, _, err := j.jiraClient.Issue.Get(params.IssueKey, nil)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Failed to get JIRA issue %s: %v", params.IssueKey, err)},
			},
		}, nil, nil
	}

	updateFields := make(map[string]interface{})

	if params.Summary != "" {
		updateFields["summary"] = []map[string]interface{}{
			{"set": params.Summary},
		}

	}
	if params.Description != "" {
		updateFields["description"] = []map[string]interface{}{
			{"set": params.Description},
		}

	}
	if len(updateFields) > 0 {
		update := map[string]interface{}{
			"update": updateFields,
		}
		_, err = j.jiraClient.Issue.UpdateIssue(issue.Key, update)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to update JIRA issue %s: %v", params.IssueKey, err)},
				},
			}, nil, nil
		}
	}

	// Note: Updating status typically requires a transition, not a direct field update.
	issueUrl := fmt.Sprintf("%s/browse/%s", j.config.BaseURL, issue.Key)
	log.Printf("Updated JIRA issue: %s\n", issueUrl)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Updated JIRA issue: %s", issueUrl)},
		},
	}, nil, nil
}
func (j *JiraMCPServer) assignIssueToUser(ctx context.Context, req *mcp.CallToolRequest, params *UpdateIssueArgs) {

}

// findJiraUser searches for a Jira user by a query string (name or email).
func (j *JiraMCPServer) findJiraUser(_ctx context.Context, query string) (*jira.User, error) {
	if query == "" {
		return nil, nil // No query, no user to find.
	}

	// Jira's user search is flexible. It can find by name, username, or email.
	users, _, err := j.jiraClient.User.Find(query)
	if err != nil {
		return nil, fmt.Errorf("error searching for user '%s': %w", query, err)
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("no user found for query '%s'", query)
	}

	// The API can return multiple matches. We'll take the first one for simplicity.
	// For better accuracy, you might want to match the email address exactly if provided.
	for _, u := range users {
		if strings.EqualFold(u.EmailAddress, query) || strings.EqualFold(u.DisplayName, query) || strings.EqualFold(u.Name, query) {
			return &u, nil
		}
	}
	return &users[0], nil // Return the first result if no exact match is found
}

// CreateJiraIssue creates a new Jira issue using the provided parameters.
// It sets a default project key if none is provided and returns the created issue key.
//
// Parameters:
//   - ctx: context for request cancellation and deadlines
//   - req: MCP CallToolRequest containing tool invocation details
//   - params: CreateJiraIssueParams with issue fields and metadata
//
// Returns:
//   - *mcp.CallToolResult: result containing the created issue key or error message
//   - any: additional data (always nil)
//   - error: always nil (errors are returned in the result content)
func (j *JiraMCPServer) CreateJiraIssue(ctx context.Context, req *mcp.CallToolRequest, params *CreateJiraIssueParams) (*mcp.CallToolResult, any, error) {
	projectKey := params.ProjectKey
	if projectKey == "" {
		projectKey = "SMS" // Default project key if not provided
	}

	var assignee *jira.User
	// Look for "assign to: <user>" in the description to assign the issue.
	if strings.Contains(strings.ToLower(params.Description), "assign to:") {
		parts := strings.SplitN(params.Description, "assign to:", 2)
		if len(parts) > 1 {
			assigneeQuery := strings.TrimSpace(strings.Split(parts[1], "\n")[0])
			log.Printf("Attempting to find and assign user: %s", assigneeQuery)
			foundUser, err := j.findJiraUser(ctx, assigneeQuery)
			if err != nil {
				log.Printf("Could not assign user: %v", err)
				// Optionally, you could return an error message to the user here.
			} else if foundUser != nil {
				log.Printf("Found user %s (%s) to assign.", foundUser.DisplayName, foundUser.EmailAddress)
				assignee = &jira.User{AccountID: foundUser.AccountID}
			} else {
				log.Printf("User '%s' not found.", assigneeQuery)
			}
		}
	} else if params.Assignee != nil && params.Assignee.AccountID != "" {
		log.Printf("Assigning user from 'assignee' parameter: %s", params.Assignee.AccountID)
		assignee = &jira.User{AccountID: params.Assignee.AccountID}
	} else {
		// Default to assigning the issue to the current user if no assignee is specified.
		currentUser, _, err := j.jiraClient.User.GetSelf()
		if err != nil {
			log.Printf("Could not get current user to self-assign: %v", err)
		} else if currentUser != nil {
			log.Printf("Defaulting assignee to current user: %s", currentUser.DisplayName)
			assignee = &jira.User{AccountID: currentUser.AccountID}
		}
	}

	issue := &jira.Issue{
		Fields: &jira.IssueFields{
			Project:     jira.Project{Key: projectKey},
			Summary:     params.Summary,
			Description: params.Description,
			Type:        jira.IssueType{Name: params.IssueType},
			Priority:    &jira.Priority{Name: params.Priority},
			Labels:      params.Labels,
			Assignee:    assignee,
		},
	}

	createdIssue, _, err := j.jiraClient.Issue.Create(issue)
	if err != nil {
		//return nil, nil, fmt.Errorf("failed to create JIRA issue: %w", err)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Failed to create JIRA issue: %v", err)},
			},
		}, nil, nil
	}
	issueUrl := fmt.Sprintf("%s/browse/%s", j.config.BaseURL, createdIssue.Key)
	log.Printf("Created JIRA issue: %s\n", issueUrl)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Created JIRA issue: %s", issueUrl)},
		},
	}, nil, nil
}

// NewJiraMCPServer creates and initializes a new JiraMCPServer instance.
// It sets up the MCP server implementation, registers Jira tools, and returns the configured server.
//
// Parameters:
//
//	config - pointer to JiraConfig containing Jira connection details
//
// Returns:
//
//	*JiraMCPServer - pointer to the initialized JiraMCPServer
//	error - error if initialization fails
func NewJiraMCPServer(config *JiraConfig) (*JiraMCPServer, error) {

	tp := jira.BasicAuthTransport{
		Username: config.Username,
		Password: config.APIToken,
	}

	jiraClient, err := jira.NewClient(tp.Client(), config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create JIRA client: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: ServerVersion,
	}, nil)

	// Initialize JiraMCPServer struct with server and config.
	jcmp := &JiraMCPServer{
		server:     server,
		config:     config,
		jiraClient: jiraClient,
	}

	// Register Jira-related tools to the MCP server.
	jcmp.addTools()

	// Return the configured JiraMCPServer instance.
	return jcmp, nil
}

func (j *JiraMCPServer) addTools() {
	mcp.AddTool(j.server, &mcp.Tool{Name: "create-jira-issue", Description: "Create a new Jira issue"}, j.CreateJiraIssue)
	mcp.AddTool(j.server, &mcp.Tool{Name: "update-jira-issue", Description: "Update an existing Jira issue"}, j.UpdateJiraIssue)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func loadConfig() (*JiraConfig, error) {
	config := &JiraConfig{
		BaseURL:    getEnv("JIRA_BASE_URL", "https://unitedmasters.atlassian.net"),
		Username:   getEnv("JIRA_USERNAME", ""),
		APIToken:   getEnv("JIRA_API_TOKEN", ""),
		ProjectKey: getEnv("JIRA_PROJECT_KEY", "SMS"),
	}
	// Validate required fields
	if config.BaseURL == "" {
		return nil, fmt.Errorf("JIRA_BASE_URL environment variable is required")
	}
	if config.Username == "" {
		return nil, fmt.Errorf("JIRA_USERNAME environment variable is required")
	}
	if config.APIToken == "" {
		return nil, fmt.Errorf("JIRA_API_TOKEN environment variable is required")
	}
	if config.ProjectKey == "" {
		return nil, fmt.Errorf("JIRA_PROJECT_KEY environment variable is required")
	}

	// Ensure BaseURL has proper format
	if !strings.HasPrefix(config.BaseURL, "http://") && !strings.HasPrefix(config.BaseURL, "https://") {
		config.BaseURL = "https://" + config.BaseURL
	}

	// Remove trailing slash if present
	config.BaseURL = strings.TrimSuffix(config.BaseURL, "/")

	return config, nil
}

func main() {
	var transport, port string
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Println("Usage: jira-mcp-server")
	flag.StringVar(&transport, "transport", "stdio", "Transport type (stdio or sse)")
	flag.StringVar(&port, "port", "3001", "Port to run the MCP server on.")

	flag.Parse()

	config, err := loadConfig()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	if config.Username == "" || config.APIToken == "" {
		log.Fatal("JIRA_USERNAME and JIRA_API_TOKEN environment variables are required")
	}

	//Log configuration for debugging (without sensitive info)
	log.Printf("Starting JIRA MCP Server with config:")
	log.Printf("  Base URL: %s", config.BaseURL)
	log.Printf("  Username: %s", config.Username)
	log.Printf("  Project Key: %s", config.ProjectKey)
	log.Printf("  API Token: %s", strings.Repeat("*", len(config.APIToken)))

	// Test JIRA connection
	log.Println("Testing JIRA connection...")
	tp := jira.BasicAuthTransport{
		Username: config.Username,
		Password: config.APIToken,
	}

	testClient, err := jira.NewClient(tp.Client(), config.BaseURL)
	if err != nil {
		log.Fatal("Failed to create JIRA client: ", err)
	}

	// Test the connection by getting current user info
	user, _, err := testClient.User.GetSelf()
	if err != nil {
		log.Fatal("Failed to authenticate with JIRA: ", err)
	}
	log.Printf("Successfully connected to JIRA as: %s (%s)", user.DisplayName, user.EmailAddress)

	jiraServer, err := NewJiraMCPServer(config)
	if err != nil {
		log.Fatal("Failed to create JIRA MCP server:", err)
	}
	log.Println("Starting JIRA MCP Server...")

	if transport == "sse" {
		log.Printf("Starting MCP server with SSE transport on port %s...", port)
		handler := mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
			//return jiraServer.server
			url := request.URL.Path
			log.Printf("Handling request for URL %s\n", url)
			switch url {
			case "/sse":
				return jiraServer.server
			default:
				return nil
			}

		})
		log.Fatal(http.ListenAndServe(":"+port, handler))
	} else {
		log.Println("Starting MCP server with STDIO transport")
		if err := jiraServer.server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatal(err)
		}
	}
}
