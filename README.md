# Coding Agent

At work I face a simple problem: all of the popular coding agents upload analytics and require lengthy review processes to use in a classified environment. They also tend to not play as well with gov-cloud based LLM API's.

The goal of this repo is to demonstrate that we can, in fact, build a simple agent which can perform tasks better than a person armed with an LLM webpage, even if the resulting agent is measurably worse than popular agents like Codex or Claude Code. While this particular agent may not ultimately be used with CUI material, it can operate as an example to show it can be done, and could be build within the span of a sprint.

## How to Use

Currently this agent uses the Grok API. It checks for the environment variable `XAI_API_KEY` before running to see if it's filled. 

Next I plan to hook this up to the AskSage API, however that functionality is yet to be built out. 
