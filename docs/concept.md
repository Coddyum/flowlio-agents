## Flowlio-Ia concept

> Translation of the original note, written in French by Maxence before any line of code existed.
> It is kept as a primary source: the rough register, the hesitations and the open questions are
> deliberate, and it has not been tidied into a specification. What was actually decided lives in
> `DESIGN-V1.md` and `decisions.md`.

### Background

Flowlio is an application I designed during a learning project, a Linear-like one, so a project
manager split into (team => project => board), a board holding columns + tasks, so a classic
kanban. You could have a backend team with several projects, an accounting team with its own
projects and so on — it was really thought of as a company manager, really.

On the task side you had several pieces of information:

- card id (a recognisable identifier, not the card's uuid, e.g. FRNT-34)
- title
- description (the description was special, because when you opened a task there was a view giving
  you a markdown editor, so tasks had extremely rich descriptions with H-\*, tables, code blocks,
  user tags and so on)
- assign_to (only let you assign to a user who had access to that board)
- deadline
  and so on

Task archiving and so on.

Flowlio recently gained an MCP, which let my code — because I work on multi-repo projects, and to
avoid having to fill in .md files from one repo to the next to share questions, requests,
integrations, API contract changes and so on between my Claude Code sessions — Claude had a very
precise workflow set of rules on how to use that MCP efficiently to work on my project and so on.

But that is not optimal, because Flowlio was originally designed for humans, not for AIs, so even
though the interface is very pleasant it is all makeshift when it comes to getting my AIs to use it
during my sessions, task tracking and so on.

### Concept + idea

My idea would be to build a project manager for AIs (Claude, Codex, OpenCode and so on). The goal
would be roughly the same: create a team (your project, Omiros for instance), inside it you have
projects (= repos), so in my case you would have the repo omiros-core (backend) and omiros-web
(frontend).

Every project would be split in two parts, a project work part and an other-project question part.
The idea would be:

part 1 = the place where the Claude of a session manages the tasks to do, it manages its
documentation for each task by itself, the priorities and so on

part 2 would be a space for the other projects to question this project. For instance project A (an
AI agent) wonders whether the backend (project B) changed the API contract of feature X, because it
no longer answers. Project A could then open a ticket on project B in a dedicated space, isolated
from project B's tasks, a bit like GitHub issues, and so project A would see the tickets the same
way and could simply answer after checking the code.

And the other way round. Beyond that, the idea would still be that the AIs stay confined (isolated)
to their team, to avoid asking questions or filing tasks on projects that are none of their
business.

The other point I would like to implement is a project's memory, because as we know one of the
flaws of AIs is their memory — they are always forced to re-read masses of things. So of course we
often set up tools like Obsidian, mempalace, .claude/memory and so on, but there is no proper
tracking really, and I have no idea how to put that in place. Still, there is one of the features I
want to build.

Another point I would quite like to do, to be seen how, is that when one of the agents has answered
an issue (ticket), it would be nice to automatically restart the Claude, Codex and so on session
that asked the question.

Surprisingly, even though it is an app for AIs, the idea is that I do not want to put any AI into
the project, because I think most things can be done deterministically.

### User interface

No frontend, no desktop application. I would like us to build a full CLI + MCP tool, so no pure
visual interface of the web-page kind or anything, except maybe for sign-up or payment, because
yes, I am not too sure how we could do that.

But I would like the app to run either locally as free open source, n8n style, or hosted by us, and
in that case a monthly subscription through Stripe.

So local usage = no account, we directly create a fake user account, no need for an email or a
password. But hosted by us, an account is needed, logically.

### Thoughts

You see, today there are plenty of incredible tools like Superset that let you run lots of AI
sessions in parallel in isolated workspaces, memory tools and so on, but I have seen nothing that
handles memory + cross-repo tasking, project, AI session efficiently. So maybe my vision of team =>
project => board with tasks + issues is not the right one, it still needs thinking about. I gave
that as an idea because it reuses what I had already built for Flowlio, but it is not necessarily
the best of ideas.

Oh yes, and the most important point in this project: you must be absolutely beyond reproach on the
handling of secrets and so on, because this project will be open source, at least for
self-hosting.
