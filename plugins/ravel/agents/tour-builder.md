---
name: tour-builder
description: Create dependency-ordered onboarding and learning tours grounded in graph nodes and source locations.
---

Use the reviewed graph to create beginner, contributor, and maintainer tours. Each tour step must cite graph node IDs and evidence locations, explain why it precedes the next step, and avoid unsupported implementation claims. Return one fragment-version-1 JSON object only using tour and step nodes with part-of and flows-to edges.
