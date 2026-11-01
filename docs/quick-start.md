# A coding agent can set up your own Polypus in one conversation

Polypus is a small gateway that runs in Docker on your own computer and speaks the OpenAI API. Your apps, scripts and agents send their AI requests to it, and it passes each one to the right model through your OpenRouter key and records the call. A coding agent such as Claude Code can set it up with you, while **you type every key into your own terminal**.

## Your tools call one address, and Polypus reaches the models

- Phoenix and HyperDX run beside the gateway and trace every call, so when an agent behaves oddly you can read what it actually sent and got back.
- Your tools always call the same address, so trying a different model means one change in Polypus rather than one in every app.
- Polypus keeps your OpenRouter key in your operating system's keyring, so none of your tools ever sees it.
- The gateway joins your Tailscale network (the private network between your own devices) and opens nothing to the internet.

## Have four things ready before you start

1. Docker running, through Docker Desktop, OrbStack or Colima on a Mac, or Docker Desktop on Windows.
2. Tailscale signed in to your own tailnet, with MagicDNS and HTTPS certificates turned on under DNS in the Tailscale admin console.
3. A Tailscale auth key from Settings, then Keys, with Ephemeral left off, which Polypus uses once to join your tailnet as its own device.
4. An OpenRouter API key from OpenRouter's Keys page, with a **credit limit** so a looping agent stops at that amount instead of spending your whole balance.

Keep the OpenRouter key to hand, because you type it into your own terminal during the setup. The agent also checks for Go 1.27 or newer, which installs the `polypus` command, and tells you if anything is missing.

## Paste one message into your coding agent

Open Claude Code, or another coding agent that can run commands, in a terminal in any folder. Paste this message, then answer its questions as it works:

```text
Help me set up my own Polypus gateway on this computer.

What Polypus is: https://polypus.dev.xynova.com/docs/quick-start/

Follow the polypus-setup skill step by step:
https://raw.githubusercontent.com/behaviorengineering/polypus/main/ai-copilots/skills/polypus-setup/SKILL.md

It follows the setup guide at https://polypus.dev.xynova.com/docs/setup/

Turn observability on in my config, so every call is traced in Phoenix and HyperDX.

Check my machine before each step and show me the plan before you deploy. Never ask me to paste a key into this chat: I will type every secret into my own terminal.
```

The agent follows the [polypus-setup skill](../ai-copilots/skills/polypus-setup/SKILL.md), which checks your machine before each step and shows you a **dry run of the deploy** before it changes anything. When it needs a key, it gives you a command such as `polypus deploy secret set OPENROUTER_API_KEY`, and you paste the key at a hidden prompt.

**The agent never needs to see your keys.** If it asks you to paste one into the chat, say no, and revoke any key that ends up there.

## You end up with one address on your tailnet

You pick a short name for your setup, such as `laptop`, and Polypus joins your tailnet as the device `polypus-laptop`. Any tool that asks for an OpenAI-style base URL and API key can then use it:

| Setting | Value |
| --- | --- |
| Base URL | `https://polypus-<name>.<tailnet>/v1`, where `<tailnet>` is your tailnet name, such as `tail1234.ts.net` |
| API key | Any text, such as `polypus`. Only your own devices can reach the gateway, so it needs no key of its own. |
| Chat model | `router/turno`, a chat route that goes to Claude Sonnet |
| Traces | Phoenix at `http://polypus-<name>.<tailnet>:6006` and HyperDX at `http://polypus-<name>.<tailnet>:8080` |

When someone else's device is on your tailnet, ask the agent to lock the gateway with a client key, as [the setup guide](setup.md#lock-the-gateway-if-others-share-your-tailnet) describes.

## The setup guide covers the same steps by hand

[Run your own Polypus on your tailnet](setup.md) walks through the same six steps, with every command and a table of what to do when something goes wrong.
