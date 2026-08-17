package client

// Client is the high-level entry point. It groups the Task / Workflow /
// Schedule / Sandbox sub-clients sharing a single Connection, mirroring the
// TS Client and Python Client classes.
type Client struct {
	Connection *Connection
	Task       *TaskClient
	Workflow   *WorkflowClient
	Schedule   *ScheduleClient
	Sandbox    *SandboxClient
}

// New wires up the sub-clients around an existing Connection. Use Dial
// (or NewConnection + New) at the top of your program; share a single
// Client across goroutines.
func New(conn *Connection) *Client {
	c := &Client{Connection: conn}
	c.Task = &TaskClient{conn: conn}
	c.Workflow = &WorkflowClient{conn: conn}
	c.Schedule = &ScheduleClient{conn: conn}
	c.Sandbox = &SandboxClient{conn: conn}
	return c
}
