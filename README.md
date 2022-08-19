# chattools
A collection of tools which are meant to send useful things over chat

This repo is pretty much useless without the g76golib library, which contains a ton of convenience functions shared across my programs.

To get the overall system working, you will want the slack-handler running, with an appropriate key put in the appropriate place. See g76golib docs for details on this.

TODO: Include sanitized config ini samples for each program.

chatcli - simple CLI interface to send a chat via the db_table abstraction layer. This is very developed with Slack, but could send messages with other chat systems as well. db_table is an agnostic plugin-based system, and chatcli only talks to that layer, not the actual chat server.

homenet-tracker - this is something I wrote to keep tabs on wireless devices joining my wifi, and report results to Slack.