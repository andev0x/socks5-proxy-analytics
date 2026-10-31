#!/usr/bin/env ruby
# frozen_string_literal: true

# Cost guard for GitHub Actions.
#
# This repo's $0 guarantee depends on three things that a reviewer can easily
# break by accident: using a billable runner label, adding an unattended
# trigger, or leaving a job without a timeout. This script fails the build if
# any of those regressions appear in .github/workflows.
#
# Run locally with: ruby .github/scripts/cost-guard.rb

require "yaml"

MAX_TIMEOUT_MINUTES = 30
ALLOWED_RUNNERS = /\A(ubuntu-latest|self-hosted(-.+)?)\z/

workflows_dir = File.expand_path("../workflows", __dir__)
workflows = Dir.glob(File.join(workflows_dir, "*.{yml,yaml}")).sort

failures = []
job_count = 0

def workflow_triggers(doc)
  # Psych parses the bare YAML key `on` as boolean true.
  doc["on"] || doc[true] || {}
end

def runner_labels(job)
  runs_on = job["runs-on"]
  labels = runs_on.is_a?(Array) ? runs_on : [runs_on]
  labels = labels.compact.map(&:to_s)
  return ["<missing>"] if labels.empty?

  expanded = labels.flat_map do |label|
    next [label] unless label.include?("matrix.")

    leaves = []
    walk = lambda do |value|
      case value
      when String then leaves << value
      when Array then value.each { |item| walk.call(item) }
      when Hash then value.each_value { |item| walk.call(item) }
      end
    end
    walk.call(job.dig("strategy", "matrix") || {})
    leaves.grep(/ubuntu|macos|windows|self-hosted/) || [label]
  end

  expanded.empty? ? labels : expanded
end

if workflows.empty?
  warn "cost-guard: no workflow files found in #{workflows_dir}"
  exit 1
end

workflows.each do |file|
  name = File.basename(file)

  begin
    doc = YAML.load_file(file)
  rescue StandardError => e
    failures << "#{name}: cannot be parsed as YAML (#{e.message})"
    next
  end

  triggers = workflow_triggers(doc)
  if triggers.is_a?(Hash)
    if triggers.key?("schedule")
      failures << "#{name}: 'schedule' runs unattended and can consume minutes forever; remove it"
    end
    if triggers.key?("pull_request_target")
      failures << "#{name}: 'pull_request_target' exposes secrets to fork code; use 'pull_request'"
    end
  end

  if doc["permissions"] == "write-all"
    failures << "#{name}: requests 'permissions: write-all'; grant only the scopes each job needs"
  end

  jobs = doc["jobs"]
  unless jobs.is_a?(Hash) && !jobs.empty?
    failures << "#{name}: has no jobs"
    next
  end

  jobs.each do |job_name, job|
    job_count += 1

    unless job.is_a?(Hash)
      failures << "#{name}: job '#{job_name}' is not a mapping"
      next
    end

    labels = runner_labels(job)

    # A self-hosted job may carry extra routing labels (linux, x64, ...).
    # It bills $0 to GitHub, so any label set containing it is acceptable.
    unless labels.any? { |label| label.start_with?("self-hosted") }
      labels.each do |label|
        next if label.match?(ALLOWED_RUNNERS)

        failures << "#{name}: job '#{job_name}' uses runner '#{label}'; " \
                    "only ubuntu-latest (1x minutes) and self-hosted ($0) are allowed"
      end
    end

    timeout = job["timeout-minutes"]
    if timeout.nil?
      failures << "#{name}: job '#{job_name}' has no timeout-minutes; a hung job bills until the 6h cap"
    elsif !timeout.is_a?(Integer) || timeout <= 0 || timeout > MAX_TIMEOUT_MINUTES
      failures << "#{name}: job '#{job_name}' timeout-minutes must be an integer in 1..#{MAX_TIMEOUT_MINUTES}"
    end
  end

  # Unpinned installers re-resolve on every run: supply-chain risk and variable cost.
  File.foreach(file).with_index(1) do |line, number|
    next unless line =~ /\b(?:go install|pip3? install|npm (?:i|install))\b/
    next unless line =~ /@(?:latest|master|main)\b/

    failures << "#{name}:#{number}: unpinned tool install; pin an exact version instead of @latest/@master/@main"
  end
end

if failures.empty?
  puts "cost-guard: OK — #{workflows.length} workflow(s), #{job_count} job(s), all on free runners with bounded timeouts."
  exit 0
end

warn "cost-guard: FAILED — #{failures.length} issue(s) that can create billable or unbounded usage:\n\n"
failures.each { |failure| warn "  - #{failure}" }
warn "\nSee docs/github-actions-cost-control.md for the policy."
exit 1
