# AI-Driven Development Framework for Go + NATS Microservices

This directory contains a comprehensive documentation framework designed to optimize AI-driven development using Claude Code for Go + NATS microservice architectures.

## Framework Overview

This documentation framework enables efficient, context-aware AI development by providing:
- **Layered Context Management**: Prevents context poisoning while providing rich guidance
- **Prompt Engineering Templates**: Structured prompts for common development scenarios
- **Best Practice Codification**: Proven patterns and anti-patterns for microservice development
- **Consistent Development Workflow**: Standardized processes across all services

## Documentation Layers

### Layer 1: Context Management (Meta-Framework)
**File: `CLAUDE_CONTEXT_MANAGER.md`**
- How Claude Code should manage context across documentation layers
- Context loading strategies and conflict resolution
- Prompt engineering best practices
- Session management for multi-service development

### Layer 2: Technical Foundation
**File: `GO_NATS_CLAUDE.md`** (Refactored)
- Pure technical patterns for Go + NATS implementation
- Performance optimization and architecture decisions  
- Connection management and messaging patterns
- No business logic or domain-specific content

### Layer 3: SaaS Business Framework
**File: `SAAS_MICROSERVICE_CLAUDE.md`**
- Domain-agnostic SaaS platform patterns
- Multi-tenancy architecture and implementation
- Generic business patterns (APIs, data modeling, security)
- Scalable SaaS operational patterns

### Layer 4: Development Standards
**Files: Multiple specialized documents**
- API design patterns and standards
- Development workflow and tooling
- Troubleshooting framework and methodologies
- Service template for repository-specific guidance

## Usage Instructions

### For New Service Development

1. **Start with Service Template**
   ```
   cp SERVICE_CLAUDE_TEMPLATE.md /path/to/new-service/CLAUDE.md
   ```

2. **Customize Service Context**
   - Fill in service-specific information
   - Define domain model and business rules
   - Document integration patterns and dependencies

3. **Reference Framework Layers**
   - Use CLAUDE_CONTEXT_MANAGER.md for context loading guidance
   - Reference SAAS_MICROSERVICE_CLAUDE.md for business patterns
   - Reference GO_NATS_CLAUDE.md for technical implementation

### For Feature Development

1. **Load Minimal Context**
   ```
   Context Required: [Service] CLAUDE.md only
   Task: Implement [feature] in existing service
   ```

2. **Add Framework Context as Needed**
   - Reference SaaS patterns for multi-tenant features
   - Reference technical patterns for performance optimization
   - Use troubleshooting framework for debugging

### For Architecture Decisions

1. **Load Full Framework Context**
   ```
   Context Required: All framework layers for architectural decisions
   Task: Design [architectural component]
   ```

2. **Follow Decision Framework**
   - Use provided decision matrices
   - Document rationale for choices
   - Update service CLAUDE.md with decisions

## File Descriptions

| File | Purpose | Usage |
|------|---------|-------|
| `CLAUDE_CONTEXT_MANAGER.md` | Meta-guidance for context management | Always reference for context loading strategy |
| `SAAS_MICROSERVICE_CLAUDE.md` | Generic SaaS business patterns | Reference for multi-tenancy, APIs, data modeling |
| `SERVICE_CLAUDE_TEMPLATE.md` | Template for service-specific docs | Copy and customize for each service repository |
| `PROMPT_ENGINEERING_FRAMEWORK.md` | Structured prompt templates | Reference for effective AI prompting |
| `API_DESIGN_PATTERNS.md` | REST API design standards | Reference for consistent API development |
| `DEVELOPMENT_WORKFLOW.md` | Development tooling and processes | Reference for build, test, deployment practices |
| `TROUBLESHOOTING_FRAMEWORK.md` | Diagnostic and resolution strategies | Reference for debugging and issue resolution |

## Context Loading Strategies

### Simple Tasks (Feature Implementation)
```
Context Required: [Service] CLAUDE.md only
```
Use when: Implementing features within well-defined service boundaries

### Moderate Tasks (Integration Development)  
```
Context Required: SAAS_MICROSERVICE_CLAUDE.md + [Service] CLAUDE.md
```
Use when: Adding multi-tenant features, API endpoints, or service integrations

### Complex Tasks (Performance Optimization)
```
Context Required: GO_NATS_CLAUDE.md + [Service] CLAUDE.md
```
Use when: Optimizing performance, debugging technical issues, or architecture changes

### Architecture Tasks (New Service Design)
```
Context Required: All framework layers for architectural decisions
```
Use when: Creating new services, major architecture changes, or technology decisions

## Best Practices

### Context Management
1. **Start Minimal**: Begin with service-specific context only
2. **Load Incrementally**: Add framework layers as needed for current task
3. **Avoid Overload**: Never load all layers unless doing architecture work
4. **Resolve Conflicts**: Service-specific decisions always override framework guidance

### Documentation Maintenance
1. **Keep Updated**: Update service CLAUDE.md as services evolve
2. **Share Patterns**: Promote successful patterns to framework level
3. **Remove Deprecated**: Clean up outdated guidance promptly
4. **Version Changes**: Track documentation versions alongside code

### Development Workflow
1. **Follow Templates**: Use structured prompt templates for consistency
2. **Document Decisions**: Capture rationale for architectural choices
3. **Test Thoroughly**: Validate framework guidance through testing
4. **Iterate Improve**: Refine framework based on development experience

## Framework Benefits

### For Development Teams
- **Consistent Quality**: Standardized patterns across all services
- **Faster Development**: Pre-built templates and decision frameworks
- **Better Onboarding**: Clear guidance for new team members
- **Reduced Errors**: Proven patterns prevent common mistakes

### For AI Development
- **Optimized Context**: Right information at the right granularity
- **Conflict Prevention**: Clear hierarchy prevents contradictory guidance  
- **Efficient Prompting**: Structured templates for effective AI interaction
- **Scalable Knowledge**: Framework grows with organizational learning

### For System Architecture
- **Architectural Consistency**: Uniform patterns across services
- **Technology Alignment**: Consistent technology choices and usage
- **Operational Excellence**: Standardized monitoring, deployment, and troubleshooting
- **Knowledge Preservation**: Tribal knowledge captured in documentation

## Extending the Framework

### Adding New Patterns
1. **Identify Pattern**: Document new successful development patterns
2. **Determine Scope**: Decide if pattern is service-specific or framework-level
3. **Update Documentation**: Add to appropriate layer
4. **Validate Usage**: Test pattern across multiple services
5. **Refine Guidance**: Improve based on usage experience

### Adding New Service Types
1. **Assess Differences**: Identify unique requirements for new service type
2. **Extend SaaS Framework**: Add generic patterns applicable across domains
3. **Create Service Template**: Develop specialized template if needed
4. **Update Context Manager**: Add guidance for new service type context loading

### Framework Versioning
- Use semantic versioning for framework releases
- Maintain backward compatibility within major versions
- Provide migration guidance for breaking changes
- Archive deprecated guidance with sunset timelines

This framework provides a solid foundation for AI-driven microservice development while remaining flexible enough to evolve with changing requirements and organizational learning.