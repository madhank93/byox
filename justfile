set quiet

byox := "bin/byox"

default: tui

# Build the engine binary
build:
    cd engine && go build -o ../bin/byox ./cmd/byox

# Clone course + tester repos, build testers, seed Go starters
setup: build
    {{byox}} setup

# Launch the TUI
tui: build
    {{byox}}

# Run tests for a course's current stage (headless)
test course: build
    {{byox}} test {{course}}

# Show progress across courses
status: build
    {{byox}} status

# Rewind a course's progress pointer to a stage (code untouched)
reset course stage: build
    {{byox}} reset {{course}} --stage {{stage}}

# Regenerate the website's catalog data from courses.yml + reference-solutions/
gen:
    cd web/gen && go run .

# Run the website locally (regenerates the catalog data first)
web: gen
    cd web && npm install && npm run dev
