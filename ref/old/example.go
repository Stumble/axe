package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
)

var skipPrefixes = []string{
	"@alva/algorithm",
	"@alva/llm",
	"@alva/external",
	"@alva/jstat",
	"@alva/technical-indicators",
}

type moduleAssets struct {
	Dir             string
	Spec            string
	CodePath        string
	TestPath        string
	DocPath         string
	DocNodifiedPath string
	EditMetaPath    string
	Code            string
	Test            string
	Doc             string
}

type nodifiedResponse struct {
	XMLName     xml.Name `xml:"NodifiedSDK"`
	Code        string   `xml:"Code"`
	Test        string   `xml:"Test"`
	DocOriginal string   `xml:"DocOriginal"`
	DocNodified string   `xml:"DocNodified"`
}

func main() {
	var (
		dryRun       bool
		model        string
		skipInterval time.Duration
	)

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[sdk_llm_editor] ")

	flag.BoolVar(&dryRun, "dry-run", false, "print the LLM response without writing files")
	flag.StringVar(&model, "model", "gpt-5", "LLM model to use")
	flag.DurationVar(
		&skipInterval,
		"skip-interval",
		30*time.Hour,
		"skip modules updated within this interval (set 0 to disable)",
	)
	flag.Parse()

	log.Printf("Starting run (dry-run=%v, model=%s, skip-interval=%s)", dryRun, model, skipInterval)

	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file loaded: %v (continuing)", err)
	}

	apiKey := strings.TrimSpace(os.Getenv("OAI_MY_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" {
		log.Fatal("set OAI_MY_KEY (preferred) or OPENAI_API_KEY with a valid token")
	}

	guideline, err := os.ReadFile("ai-code/code_review_nodified_sdk.md")
	if err != nil {
		log.Fatalf("read guideline: %v", err)
	}
	log.Printf("Loaded guideline (%d bytes)", len(guideline))

	modules, err := discoverModules("assets")
	if err != nil {
		log.Fatalf("discover modules: %v", err)
	}
	log.Printf("Discovered %d module directories containing code.js", len(modules))

	if len(modules) == 0 {
		log.Println("no modules with code.js found under assets")
		return
	}

	client := openai.NewClient(apiKey)
	ctx := context.Background()

	for _, module := range modules {
		log.Printf("Evaluating module %s (dir=%s)", module.Spec, module.Dir)
		skip, meta, err := shouldSkipModule(module, skipInterval)
		if err != nil {
			log.Printf("skipping %s due to metadata error: %v", module.Spec, err)
			continue
		}
		if skip {
			editedAt := "unknown time"
			if meta != nil && !meta.EditedAt.IsZero() {
				editedAt = meta.EditedAt.Format(time.RFC3339)
			}
			log.Printf("skipping %s: last edited at %s", module.Spec, editedAt)
			continue
		}

		if meta == nil || meta.EditedAt.IsZero() {
			log.Printf("No recent metadata for %s; treating as fresh module", module.Spec)
		} else {
			log.Printf("Previous edit for %s was at %s (older than skip window)", module.Spec, meta.EditedAt.Format(time.RFC3339))
		}
		log.Printf(
			"Context sizes for %s: code=%dB test=%dB doc=%dB",
			module.Spec,
			len(module.Code),
			len(module.Test),
			len(module.Doc),
		)

		log.Printf("processing %s", module.Spec)
		resp, err := requestNodified(ctx, client, model, string(guideline), module)
		if err != nil {
			log.Printf("skipping %s: %v", module.Spec, err)
			continue
		}

		if dryRun {
			log.Printf("dry-run active: skipping file writes for %s", module.Dir)
			continue
		}

		if err := writeModuleFiles(module, resp, model); err != nil {
			log.Printf("failed to write files for %s: %v", module.Spec, err)
			continue
		}
		log.Printf("Completed update for %s", module.Spec)
	}
}

func discoverModules(root string) ([]moduleAssets, error) {
	var modules []moduleAssets

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if rel != "." {
			for _, prefix := range skipPrefixes {
				if rel == prefix || strings.HasPrefix(rel, prefix+string(filepath.Separator)) {
					if d.IsDir() {
						log.Printf("Skipping directory %s (matched prefix %s)", path, prefix)
						return filepath.SkipDir
					}
					return nil
				}
			}
		}

		if !d.IsDir() {
			return nil
		}

		codePath := filepath.Join(path, "code.js")
		if _, err := os.Stat(codePath); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}

		testPath := filepath.Join(path, "test.js")
		testContent, _ := os.ReadFile(testPath) // test.js optional

		docPath := filepath.Join(path, "doc")
		docContent, _ := os.ReadFile(docPath) // doc optional

		spec := deriveSpec(rel)

		codeContent, err := os.ReadFile(codePath)
		if err != nil {
			return err
		}

		modules = append(modules, moduleAssets{
			Dir:             path,
			Spec:            spec,
			CodePath:        codePath,
			TestPath:        testPath,
			DocPath:         docPath,
			DocNodifiedPath: filepath.Join(path, "doc_nodified"),
			EditMetaPath:    filepath.Join(path, "llm_edit.json"),
			Code:            string(codeContent),
			Test:            string(testContent),
			Doc:             string(docContent),
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	return modules, nil
}

func deriveSpec(rel string) string {
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 {
		return rel
	}
	return fmt.Sprintf("%s:%s", strings.Join(parts[:len(parts)-1], "/"), parts[len(parts)-1])
}

type editMetadata struct {
	EditedAt time.Time `json:"edited_at"`
	Model    string    `json:"model,omitempty"`
}

func shouldSkipModule(
	module moduleAssets,
	skipInterval time.Duration,
) (bool, *editMetadata, error) {
	if skipInterval <= 0 {
		return false, &editMetadata{}, nil
	}

	data, err := os.ReadFile(module.EditMetaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, &editMetadata{}, nil
		}
		return false, nil, fmt.Errorf("read %s: %w", module.EditMetaPath, err)
	}

	var meta editMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return false, nil, fmt.Errorf("parse %s: %w", module.EditMetaPath, err)
	}

	if meta.EditedAt.IsZero() {
		if info, statErr := os.Stat(module.EditMetaPath); statErr == nil {
			meta.EditedAt = info.ModTime()
		}
	}

	if meta.EditedAt.IsZero() {
		return false, &meta, nil
	}

	age := time.Since(meta.EditedAt)
	if age < skipInterval {
		return true, &meta, nil
	}

	return false, &meta, nil
}

func requestNodified(
	ctx context.Context,
	client *openai.Client,
	model, guideline string,
	module moduleAssets,
) (*nodifiedResponse, error) {
	log.Printf("Preparing LLM request for %s using model %s", module.Spec, model)
	messages := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: `You are GPT-5 assisting with refactoring SDK modules. Always respond with XML in the exact format:
<NodifiedSDK>
  <Code><![CDATA[...]]></Code>
  <Test><![CDATA[...]]></Test>
  <DocOriginal><![CDATA[...]]></DocOriginal>
  <DocNodified><![CDATA[...]]></DocNodified>
</NodifiedSDK>
Do not add any commentary outside the XML. Ensure each CDATA block only includes the file content.`,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: buildUserPrompt(guideline, module),
		},
	}

	req := openai.ChatCompletionRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0,
	}

	log.Printf("Opening LLM stream for %s", module.Spec)
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm stream start: %w", err)
	}
	defer stream.Close()

	fmt.Printf("\n=== Streaming LLM output for %s ===\n", module.Spec)
	var builder strings.Builder

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("llm stream recv: %w", err)
		}

		if len(response.Choices) == 0 {
			continue
		}

		for _, choice := range response.Choices {
			if chunk := choice.Delta.Content; chunk != "" {
				fmt.Print(chunk)
				builder.WriteString(chunk)
			}
			if choice.FinishReason != "" && choice.FinishReason != openai.FinishReasonStop {
				log.Printf(
					"LLM reported finish reason %q while streaming %s",
					choice.FinishReason,
					module.Spec,
				)
			}
		}
	}

	fmt.Printf("\n=== End of LLM output for %s ===\n\n", module.Spec)

	payload := strings.TrimSpace(builder.String())
	if payload == "" {
		return nil, errors.New("empty LLM response")
	}
	log.Printf("LLM response length for %s: %d bytes", module.Spec, len(payload))

	decoded, err := parseXML(payload)
	if err != nil {
		return nil, fmt.Errorf("parse xml: %w", err)
	}
	log.Printf("XML parsing complete for %s", module.Spec)

	return decoded, nil
}

func buildUserPrompt(guideline string, module moduleAssets) string {
	builder := &strings.Builder{}
	fmt.Fprintf(
		builder,
		"Follow the nodified SDK guideline to refactor the module %s.\n",
		module.Spec,
	)
	builder.WriteString("Guideline:\n<<<GUIDELINE>>>\n")
	builder.WriteString(guideline)
	builder.WriteString("\n<<<END_GUIDELINE>>>\n\n")
	fmt.Fprintf(builder, "Existing files for %s:\n", module.Spec)

	builder.WriteString("\n--- code.js ---\n")
	builder.WriteString(module.Code)

	builder.WriteString("\n--- test.js ---\n")
	if strings.TrimSpace(module.Test) == "" {
		builder.WriteString("// file not present\n")
	} else {
		builder.WriteString(module.Test)
	}

	builder.WriteString("\n--- doc ---\n")
	if strings.TrimSpace(module.Doc) == "" {
		builder.WriteString("// file not present\n")
	} else {
		builder.WriteString(module.Doc)
	}

	builder.WriteString("\n\nProduce updated files following the XML contract.")

	return builder.String()
}

func parseXML(payload string) (*nodifiedResponse, error) {
	payload = strings.TrimSpace(payload)
	if !strings.HasPrefix(payload, "<") {
		return nil, errors.New("response does not appear to be XML")
	}

	var result nodifiedResponse
	if err := xml.Unmarshal([]byte(payload), &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func writeModuleFiles(module moduleAssets, resp *nodifiedResponse, model string) error {
	updates := []struct {
		path    string
		content string
	}{
		{module.CodePath, ensureTrailingNewline(resp.Code)},
		{module.TestPath, ensureTrailingNewline(resp.Test)},
		{module.DocPath, ensureTrailingNewline(resp.DocOriginal)},
		{module.DocNodifiedPath, ensureTrailingNewline(resp.DocNodified)},
	}

	for _, update := range updates {
		log.Printf("Writing %s", update.path)
		if err := os.WriteFile(update.path, []byte(update.content), 0o644); err != nil { //nolint:gosec
			return fmt.Errorf("write %s: %w", update.path, err)
		}
	}

	meta := editMetadata{
		EditedAt: time.Now().UTC(),
		Model:    model,
	}

	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := os.WriteFile(module.EditMetaPath, append(metaBytes, '\n'), 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("write %s: %w", module.EditMetaPath, err)
	}
	log.Printf("Recorded metadata for %s at %s", module.Spec, module.EditMetaPath)

	return nil
}

func ensureTrailingNewline(input string) string {
	trimmed := strings.TrimRight(input, "\r")
	if strings.HasSuffix(trimmed, "\n") {
		return trimmed
	}
	return trimmed + "\n"
}
