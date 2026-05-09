package sdk

// Client is the high-level entry point. It groups the Task / Workflow /
// Schedule sub-clients sharing a single Connection, mirroring the TS
// Client and Python Client classes.
type Client struct {
	Connection *Connection
	Task       *TaskClient
	Workflow   *WorkflowClient
	Schedule   *ScheduleClient
}

// NewClient wires up the sub-clients around an existing Connection.
func NewClient(conn *Connection) *Client {
	c := &Client{Connection: conn}
	c.Task = &TaskClient{conn: conn}
	c.Workflow = &WorkflowClient{conn: conn}
	c.Schedule = &ScheduleClient{conn: conn}
	return c
}
