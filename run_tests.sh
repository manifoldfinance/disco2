#!/bin/bash

# Exit immediately if a command exits with a non-zero status.
set -e

# Define variables
PROJECT_ROOT="/Users/janitor/monzo"
COVERAGE_DIR="$PROJECT_ROOT/coverage"
COVERAGE_FILE="$COVERAGE_DIR/coverage.total.out"
COVERAGE_HTML_REPORT="$COVERAGE_DIR/coverage.html"

# Find all directories containing Go test files
# Exclude the proto directory as it contains generated code without tests
SERVICE_DIRS=$(find . -type f -name "*_test.go" -print0 | xargs -0 -I {} dirname {} | sort -u | grep -v "^./proto")

# Function to run unit tests and collect coverage for a given directory
run_unit_tests() {
    local dir="$1"
    local service_name=$(basename "$dir")
    local coverage_output="$COVERAGE_DIR/${service_name}.coverage.out"

    echo "--> Running unit tests for $service_name ($dir)"
    # Run tests with coverage, appending to a service-specific file
    # Use -covermode=atomic for accurate coverage in concurrent tests
    # ./... ensures tests in subdirectories (if any) are also run
    go test -v -coverprofile="$coverage_output" -covermode=atomic "$dir"/...

    echo "    Coverage data saved to $coverage_output"
}

# Function to combine coverage files
combine_coverage() {
    echo "--> Combining coverage data..."
    # Ensure the coverage directory exists
    mkdir -p "$COVERAGE_DIR"
    # Clear previous total coverage file
    > "$COVERAGE_FILE"

    # Append coverage data from each service file
    for service_coverage_file in "$COVERAGE_DIR"/*.coverage.out; do
        if [ -f "$service_coverage_file" ]; then
            # The first file needs the header, subsequent files just append data
            if [ ! -s "$COVERAGE_FILE" ]; then
                 cat "$service_coverage_file" >> "$COVERAGE_FILE"
            else
                 # Append content excluding the header line (mode: atomic)
                 grep -v "^mode:" "$service_coverage_file" >> "$COVERAGE_FILE"
            fi
        fi
    done

    if [ -s "$COVERAGE_FILE" ]; then
        echo "    Combined coverage data saved to $COVERAGE_FILE"
    else
        echo "    No coverage data found to combine."
        exit 1 # Exit if no coverage data was collected
    fi
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

# Run unit tests for each service
for dir in $SERVICE_DIRS; do
    run_unit_tests "$dir"
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

echo "Test execution finished."
