#!/usr/bin/env perl
use strict;
use lib '/data/responder/lib/';
use warnings FATAL => 'all';
use Responder;
use JSON;
use Data::Dumper;

sub GiveUsage() {
    print "Usage:\n";
    print " \`active\` - list the active devices.\n";
    print " \`untagged\` - list the devices with no nickname.\n";
    print " \`expired\` - list expired devices.\n";
    print " \`tag <macaddr> <name>\` - tag a device with that specific nickname.\n";
    exit;
}

sub QueryDhcpClients($) {
    my ($Arg)=@_;
    $Arg=lc($Arg);
    my $Where;
    if($Arg eq 'active') {
        $Where = 'where expired=false';
    } elsif($Arg eq 'expired') {
        $Where='where expired=true';
    } elsif($Arg eq 'untagged') {
        $Where='where nickname is null';
    } else {
        GiveUsage();
    }
    Responder::LoadConfig();
    my $dbh=Responder::ConnectToDbViaKey('dhcptracker.dbsection');
    my $sth=$dbh->prepare("select mac,lastip,clienthostname,lastseen,expiration from dhcphosts $Where order by mac;");
    $sth->execute;
    my ($mac,$lastip,$client,$lastseen,$expiration);
    $sth->bind_columns(\$mac,\$lastip,\$client,\$lastseen,\$expiration);
    while($sth->fetch)
    {
        if(!$client)
        {
            $client='unnamed';
        }
        printf "%s (%s) last seen %s at IP %s (expires %s)\n",
            $mac, $client, $lastseen, $lastip, $expiration;
    }
    $sth->finish;
}

sub TagDevice() {
    Responder::LoadConfig();
    my $dbh=Responder::ConnectToDbViaKey('dhcptracker.dbsection');
    my $sth=$dbh->prepare("");
    $sth->execute;
    my ($mac,$lastip,$client,$lastseen,$expiration);
    $sth->bind_columns(\$mac,\$lastip,\$client,\$lastseen,\$expiration);
    while($sth->fetch)

    exit;
}

our $debug=0;

my $ParamCount=@ARGV;

Responder::SetIniPath("/data/baytor/homenet.ini");

if ($ParamCount==0) {
    GiveUsage();
}

if ($ARGV[0] eq 'untagged') {
    print "*Clients, active and expired, without a tag:*\n";
    QueryDhcpClients('untagged');
} elsif ($ARGV[0] eq 'expired') {
    print "*Expired clients:*\n";
    QueryDhcpClients('active');
} elsif ($ARGV[0] eq 'active') {
    print "*Active clients:*\n";
    QueryDhcpClients('active');
} elsif ($ARGV[0] eq 'tag') {
    TagDevice();
} else {
    GiveUsage();
}