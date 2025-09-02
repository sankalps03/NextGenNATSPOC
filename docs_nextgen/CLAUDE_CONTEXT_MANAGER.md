# Claude Context Management Framework

This document defines how Claude Code should intelligently manage context across documentation layers to prevent context poisoning and optimize development effectiveness.

## Context Loading Hierarchy

### Layer Priority (Highest to Lowest)
1. **Service-Specific Context** - Repository CLAUDE.md (overrides all)
2. **SaaS Framework Context** - SAAS_MICROSERVICE_CLAUDE.md (business patterns)
3. **Technical Foundation** - GO_NATS_CLAUDE.md (technical patterns only)
4. **Context Management** - This file (meta-guidance)

### Context Loading Strategy

#### Rule 1: Start Minimal, Load Contextually
- Begin with service-specific CLAUDE.md only
- Load higher layers only when patterns/decisions needed
- Never load all layers simultaneously

#### Rule 2: Conflict Resolution
- Service-specific decisions ALWAYS override framework guidance
- Explicit service decisions take precedence over generic patterns
- Document conflicts in service CLAUDE.md with rationale

#### Rule 3: Context Boundaries
- Technical patterns (GO_NATS_CLAUDE.md): Infrastructure, performance, architecture
- Business patterns (SAAS_MICROSERVICE_CLAUDE.md): Multi-tenancy, APIs, data modeling
- Service patterns (CLAUDE.md): Domain logic, service boundaries, integrations

## Prompt Engineering Framework

### Context Loading Commands
Use these specific phrases to load appropriate context:

**Load Technical Context**: "Reference GO_NATS_CLAUDE.md for technical patterns"
**Load SaaS Context**: "Reference SAAS_MICROSERVICE_CLAUDE.md for business patterns"  
**Load Full Context**: "Reference all framework layers for architectural decisions"

### Development Task Templates

#### New Service Creation
```
Context Required: Service CLAUDE.md + SAAS framework + technical foundation
Task: Create new microservice for [domain]
Requirements: [specific requirements]
Integration Points: [other services]
```

#### Feature Implementation  
```
Context Required: Service CLAUDE.md only
Task: Implement [feature] in existing service
Business Rules: [domain rules]
Technical Constraints: [performance, security requirements]
```

#### Performance Optimization
```
Context Required: GO_NATS_CLAUDE.md + service CLAUDE.md
Task: Optimize [component] performance
Current Metrics: [baseline measurements]
Target Metrics: [performance goals]
```

#### Integration Development
```
Context Required: SAAS framework + service CLAUDE.md  
Task: Integrate with [external service]
Integration Pattern: [message-based, API-based, event-driven]
Data Exchange: [message formats, protocols]
```

## Context Window Optimization

### Information Density Rules
- **High Density**: Principles, patterns, decision frameworks
- **Medium Density**: Examples, templates, common scenarios  
- **Low Density**: Detailed code, verbose explanations, redundant information

### Context Refresh Indicators
Refresh context when:
- Switching between different services
- Moving from implementation to architecture tasks
- Encountering conflicts between layers
- Performance requirements change significantly

### Context Poisoning Prevention
- Never mix domain-specific examples across services
- Avoid loading deprecated or conflicting guidance
- Clear context when switching development domains
- Use explicit context boundaries in prompts

## Development Workflow Integration

### Session Management
1. **Session Start**: Load service-specific CLAUDE.md
2. **Task Analysis**: Determine additional context needs
3. **Context Loading**: Load only required layers
4. **Development**: Execute with focused context
5. **Session End**: Document any context conflicts or updates needed

### Multi-Service Development
When working across services:
- Treat each service as separate context domain
- Load service-specific context for current focus
- Use framework layers only for cross-service patterns
- Document integration patterns in both services

### Context Evolution
- Update service CLAUDE.md with new patterns discovered
- Escalate common patterns to framework level
- Remove deprecated guidance promptly
- Maintain clear versioning for context changes

## AI Assistant Guidelines

### Context Usage Validation
Before proceeding with any development task:
1. Identify minimum context required
2. Check for potential conflicts between layers  
3. Verify service-specific overrides
4. Confirm business vs technical pattern separation

### Quality Assurance
- Always validate context relevance to current task
- Prefer specific over generic guidance
- Challenge assumptions from framework layers
- Maintain context boundaries strictly

### Performance Monitoring
Track context effectiveness:
- Development velocity with different context loads
- Error rates with various context combinations
- Context conflict frequency
- Developer satisfaction with guidance quality

This framework ensures Claude Code operates with optimal context while preventing information overload and conflicting guidance.