# chattools
A collection of tools which are meant to send useful things over chat

This repo is pretty much useless without the g76golib library, which contains a ton of convenience functions shared across my programs.

To get the overall system working, you will want the slack-handler running, with an appropriate key put in the appropriate place. See g76golib docs for details on this.

TODO: Include sanitized config ini samples for each program.

chatcli - simple CLI interface to send a chat via the db_table abstraction layer. This is very developed with Slack, but could send messages with other chat systems as well. db_table is an agnostic plugin-based system, and chatcli only talks to that layer, not the actual chat server.

check-slack-emoji - For monitoring Slack instances, watching for offensive emoji being uploaded. The output is messages going to a particular channel on your Slack instance, notifying you of any emoji being added, changed, or deleted on your instance. This CAN become a source of contention, or competition, or simply ick that you wish you'd never noticed your users uploading. But, better to see it when they upload it than later on when someone else has already filed a complaint.

homenet-tracker - this is something I wrote to keep tabs on wireless devices joining my wifi, and report results to Slack.