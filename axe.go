package axe

const (
	ModelGPT5 = "gpt-5"
	ModelGPT4o = "gpt-4o"
	ModelGPT4oMini = "gpt-4o-mini"
)

type Runner struct {
	Instruction string
	Files       map[string]string
	TestCmd        string
	Model       string
}

// Using eino react
func (r *Runner) Run() error {
	// implement this. It should read the files into a structured xml representation, and pass it and the instruction to
	// a llm.
	// The llm shall edit the code based on the need and then run the tests via the test command.
	// the run results shall be collected and sent to the llm for feedback.
	// until the code and tests follow the instruction, the loop shall continue.
	// These should be implemented in a reactive loop using eino react agent. see ref/eino/eino_workflow.go for example.
}


func MustLoadFiles(pattern string) map[string]string {
	// implement this. it should load all files in the pattern.
}
