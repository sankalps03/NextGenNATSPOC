# Prompt Engineering Framework for Go + NATS Microservices

This document provides structured prompt templates and guidelines for effective AI-driven development using Claude Code.

## Prompt Structure Guidelines

### Context Loading Template
Use this structure for all development prompts:

```
Context Required: [Specify which documentation layers to reference]
Task Type: [Development/Architecture/Debug/Optimization]
Service: [Service name if applicable]
Complexity: [Simple/Moderate/Complex]

Task: [Clear, specific description]
Requirements: [Functional and non-functional requirements]
Constraints: [Technical, business, or resource constraints]
Success Criteria: [How to validate completion]
```

## Development Task Templates

### New Service Creation
```
Context Required: SAAS_MICROSERVICE_CLAUDE.md + GO_NATS_CLAUDE.md
Task Type: Architecture
Service: [new-service-name]
Complexity: Complex

Task: Design and implement new microservice for [business capability]
Requirements:
- Handle [specific business operations]
- Integrate with [list of other services]  
- Support [tenant requirements]
- Performance: [throughput/latency requirements]

Constraints:
- Must follow established SaaS patterns
- Database: [constraints if any]
- Message patterns: [specific NATS patterns required]

Success Criteria:
- Service passes integration tests
- Meets performance requirements
- Follows established patterns
- Complete documentation
```

### Feature Implementation
```
Context Required: [Service] CLAUDE.md only
Task Type: Development  
Service: [existing-service-name]
Complexity: [Simple/Moderate/Complex]

Task: Implement [feature name] in existing service
Requirements:
- [Functional requirement 1]
- [Functional requirement 2]
- [Non-functional requirements]

Business Rules:
- [Rule 1 and enforcement approach]
- [Rule 2 and enforcement approach]

Integration:
- [Required changes to message contracts]
- [Impact on other services]

Success Criteria:
- [Measurable outcomes]
- [Test coverage expectations]
```

### Performance Optimization
```
Context Required: GO_NATS_CLAUDE.md + [Service] CLAUDE.md
Task Type: Optimization
Service: [service-name]
Complexity: Moderate

Task: Optimize [component/operation] performance
Current Metrics:
- [Baseline measurements]
- [Problem areas identified]

Target Metrics:
- [Performance goals]
- [Resource utilization targets]

Constraints:
- [Cannot change existing APIs]
- [Memory/CPU limitations]
- [Deployment constraints]

Success Criteria:
- Meet target performance metrics
- No functional regression
- Maintain existing SLAs
```

### Integration Development
```
Context Required: SAAS_MICROSERVICE_CLAUDE.md + [Service] CLAUDE.md
Task Type: Development
Service: [service-name]
Complexity: Moderate

Task: Integrate with [external service/system]
Integration Pattern: [Event-driven/API-based/Hybrid]
Requirements:
- [Data exchange requirements]
- [Protocol specifications]
- [Error handling needs]

Security:
- [Authentication requirements]
- [Data protection needs]

Success Criteria:
- Successful data exchange
- Proper error handling
- Security compliance
- Complete documentation
```

### Bug Investigation and Fix
```
Context Required: [Service] CLAUDE.md
Task Type: Debug
Service: [service-name]
Complexity: [Simple/Moderate/Complex]

Task: Investigate and fix [bug description]
Symptoms:
- [Observable behavior]
- [Error messages/logs]
- [Affected operations]

Context:
- [When issue occurs]
- [Environmental factors]
- [Recent changes]

Expected Behavior:
- [What should happen instead]

Success Criteria:
- Root cause identified
- Fix implemented and tested
- Prevention measures added
- Documentation updated
```

## Architecture Decision Templates

### Technology Choice Evaluation
```
Context Required: GO_NATS_CLAUDE.md + SAAS_MICROSERVICE_CLAUDE.md
Task Type: Architecture
Service: [applicable services]
Complexity: Complex

Decision: Choose [technology type] for [use case]
Options Considered:
- Option 1: [pros/cons/fit]
- Option 2: [pros/cons/fit]
- Option 3: [pros/cons/fit]

Evaluation Criteria:
- Performance requirements: [specifics]
- Scalability needs: [requirements]  
- Team expertise: [current capabilities]
- Operational overhead: [maintenance burden]

Constraints:
- Budget: [limitations]
- Timeline: [delivery expectations]
- Compliance: [regulatory requirements]

Success Criteria:
- Meets all technical requirements
- Within operational constraints
- Team can effectively maintain
- Aligns with architecture principles
```

### Service Boundary Definition
```
Context Required: SAAS_MICROSERVICE_CLAUDE.md
Task Type: Architecture
Service: [service-boundary-decision]
Complexity: Complex

Decision: Define service boundaries for [business domain]
Domain Analysis:
- Business capabilities: [list and relationships]
- Data entities: [ownership and relationships]
- Team structure: [team boundaries and expertise]

Considerations:
- Coupling: [acceptable coupling levels]
- Consistency: [consistency requirements]
- Performance: [cross-service communication needs]
- Autonomy: [deployment and development independence]

Success Criteria:
- Clear service responsibilities
- Minimal coupling between services
- Optimal team ownership alignment
- Scalable service interaction patterns
```

## Code Quality Templates

### Code Review Request
```
Context Required: [Service] CLAUDE.md
Task Type: Review
Service: [service-name]
Complexity: [Simple/Moderate/Complex]

Task: Review code changes for [feature/fix description]
Change Scope:
- Files modified: [list key files]
- New functionality: [describe additions]
- Impact areas: [affected components]

Review Focus:
- Code quality: [adherence to patterns]
- Test coverage: [adequacy and quality]
- Performance: [potential impacts]
- Security: [security considerations]
- Documentation: [updates needed]

Success Criteria:
- Code follows established patterns
- Adequate test coverage
- No security vulnerabilities
- Performance impact acceptable
- Documentation complete
```

### Refactoring Planning
```
Context Required: [Service] CLAUDE.md + GO_NATS_CLAUDE.md
Task Type: Refactoring
Service: [service-name]
Complexity: Moderate

Task: Refactor [component/module] for [improvement goal]
Current Issues:
- [Code quality problems]
- [Performance issues]
- [Maintenance difficulties]

Refactoring Goals:
- [Improvement objective 1]
- [Improvement objective 2]

Constraints:
- No API breaking changes
- Maintain existing functionality
- Minimize service downtime

Success Criteria:
- Improved code quality metrics
- Better performance characteristics
- Easier maintenance and extension
- All existing tests pass
```

## Troubleshooting Templates

### System Issue Investigation
```
Context Required: [Service] CLAUDE.md
Task Type: Debug
Service: [affected-service]
Complexity: [Simple/Moderate/Complex]

Task: Investigate system issue [problem description]
Symptoms:
- Error messages: [specific errors observed]
- Performance degradation: [metrics and timing]
- Failed operations: [which operations are failing]

System State:
- Recent deployments: [changes made recently]
- Resource utilization: [CPU/memory/network]
- External dependencies: [status of dependencies]

Investigation Approach:
- Log analysis: [relevant log sources]
- Metric review: [key metrics to examine]
- Trace analysis: [distributed tracing data]

Success Criteria:
- Root cause identified
- Impact fully understood
- Resolution plan created
- Prevention measures defined
```

## Best Practices for Prompt Engineering

### Prompt Clarity Guidelines
1. **Be Specific**: Avoid ambiguous language and provide concrete examples
2. **Set Context Boundaries**: Clearly specify which documentation to reference
3. **Define Success**: Always include measurable success criteria
4. **Include Constraints**: Explicitly state limitations and requirements
5. **Provide Background**: Give enough context for informed decisions

### Context Management
1. **Start Minimal**: Begin with service-specific context only
2. **Load Incrementally**: Add framework context as needed
3. **Avoid Conflicts**: Explicitly resolve contradictions between layers
4. **Update Context**: Refresh context when switching between services
5. **Validate Relevance**: Ensure loaded context applies to current task

### Iterative Refinement
1. **Start Broad**: Begin with high-level approach
2. **Drill Down**: Focus on specific implementation details
3. **Validate Assumptions**: Confirm understanding before proceeding
4. **Test Incrementally**: Validate each step before moving forward
5. **Document Decisions**: Capture rationale for future reference

### Common Prompt Patterns to Avoid

#### Anti-Pattern: Vague Requirements
```
Bad: "Make the service faster"
Good: "Reduce API response time from 200ms to 50ms while maintaining current throughput"
```

#### Anti-Pattern: Missing Context
```
Bad: "Add authentication"
Good: "Add JWT-based authentication following our SaaS multi-tenant patterns"
```

#### Anti-Pattern: No Success Criteria
```
Bad: "Fix the bug"
Good: "Fix authentication timeout issue so users can maintain 8-hour sessions"
```

#### Anti-Pattern: Context Overload
```
Bad: "Reference all documentation"
Good: "Reference SERVICE_CLAUDE.md for business rules and GO_NATS_CLAUDE.md for connection patterns"
```

This framework ensures consistent, effective communication with Claude Code for optimal development outcomes.