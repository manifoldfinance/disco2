#!/bin/bash

# Exit immediately if a command exits with a non-zero status.
set -e

# Define variables
PROJECT_ROOT="/Users/janitor/monzo"
COVERAGE_DIR="$PROJECT_ROOT/coverage"
COVERAGE_FILE="$COVERAGE_DIR/coverage.total.out"
COVERAGE_HTML_REPORT="$COVERAGE_DIR/coverage.html"

# Identify directories that could contain Go test files
# In the refactored structure these are primarily in cmd/ and internal/
# Exclude pkg/pb as it contains generated code without tests
CMD_DIRS=$(find cmd -type d -name "[^.]?*" -not -path "*/vendor/*")
INTERNAL_DIRS=$(find internal -type d -name "[^.]?*" -not -path "*/vendor/*")

TEST_DIRS="$CMD_DIRS $INTERNAL_DIRS"

# Function to run unit tests and collect coverage for a given directory
run_unit_tests() {
    local dir="$1"
    local service_name=$(echo "$dir" | tr '/' '_')
    local coverage_output="$COVERAGE_DIR/${service_name}.coverage.out"

    echo "--> Running unit tests for $dir"
    # Run tests with coverage, appending to a service-specific file
    # Use -covermode=atomic for accurate coverage in concurrent tests
    # ./... ensures tests in subdirectories (if any) are also run
    go test -v -coverprofile="$coverage_output" -covermode=atomic "./$dir"/...

    if [ $? -eq 0 ]; then
        echo "    Coverage data saved to $coverage_output"
    else
        echo "    Tests failed for $dir"
        return 1
    fi
}

# Function to combine coverage files
combine_coverage() {
    echo "--> Combining coverage data..."
    # Ensure the coverage directory exists
    mkdir -p "$COVERAGE_DIR"
    # Clear previous total coverage file
    > "$COVERAGE_FILE"

    # Find all coverage files and combine them
    COVERAGE_FILES=$(find "$COVERAGE_DIR" -name "*.coverage.out")
    if [ -z "$COVERAGE_FILES" ]; then
        echo "    No coverage data found to combine."
        return 1
    fi

    # Write header from first file
    head -1 $(echo "$COVERAGE_FILES" | head -1) > "$COVERAGE_FILE"
    
    # Append data (excluding headers) from all files
    for f in $COVERAGE_FILES; do
        tail -n +2 "$f" >> "$COVERAGE_FILE"
    done

    echo "    Combined coverage data saved to $COVERAGE_FILE"
}

# Function to generate HTML coverage report
generate_coverage_report() {
    echo "--> Generating HTML coverage report..."
    go tool cover -html="$COVERAGE_FILE" -o "$COVERAGE_HTML_REPORT"
    echo "    HTML report generated: $COVERAGE_HTML_REPORT"
}

# --- Main Workflow ---

echo "Starting test execution..."

# Clean up previous coverage data
echo "--> Cleaning up previous coverage data..."
rm -rf "$COVERAGE_DIR"
mkdir -p "$COVERAGE_DIR"

# Track if any tests failed
TEST_FAILURES=0

# Run unit tests for each directory
for dir in $TEST_DIRS; do
    if [ -d "$dir" ]; then
        if ! run_unit_tests "$dir"; then
            TEST_FAILURES=$((TEST_FAILURES + 1))
        fi
    fi
done

# Combine coverage data
combine_coverage

# Check if running in a CI environment (e.g., GitHub Actions, Travis CI, Jenkins)
if [ -n "$CI" ]; then
    echo "--> Running in CI environment."
    # In CI, we typically don't open the report, but the coverage file is available
    # for upload to a coverage service (e.g., Codecov, Coveralls)
    echo "Test execution and coverage collection complete. Combined coverage file: $COVERAGE_FILE"
    # Add commands here to upload $COVERAGE_FILE to your CI coverage service
    # Example (requires installing the service's uploader):
    # <your_coverage_uploader> -f "$COVERAGE_FILE"
else
    echo "--> Running in local environment."
    # Generate and open the HTML report locally
    generate_coverage_report
    echo "Opening HTML coverage report in browser..."
    # Use 'open' on macOS, 'xdg-open' on Linux, 'start' on Windows (requires Git Bash or similar)
    # This is a best effort attempt, user might need to open manually
    case "$(uname -s)" in
        Darwin)  open "$COVERAGE_HTML_REPORT" ;;
        Linux)   xdg-open "$COVERAGE_HTML_REPORT" ;;
        MINGW*)  start "" "$COVERAGE_HTML_REPORT" ;;
        *)       echo "Could not automatically open report. Please open $COVERAGE_HTML_REPORT manually." ;;
    esac
fi

# Exit with non-zero status if any tests failed
if [ $TEST_FAILURES -gt 0 ]; then
    echo "Test execution finished with $TEST_FAILURES failures."
    exit 1
else
    echo "Test execution finished successfully."
fi
