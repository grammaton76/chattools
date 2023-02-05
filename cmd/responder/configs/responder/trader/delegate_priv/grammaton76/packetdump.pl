#!/usr/bin/env perl
use strict;
use warnings FATAL => 'all';
use FindBin;
sub LibPath() { my $Caw=$FindBin::Bin; $Caw=~s/\/baytor\/responder\/.*/\/baytor\/responder\/lib/; return $Caw; }
use lib LibPath();
use JSON;
use Data::Dumper;
use Responder;

my %IPCdata;

my $RemoteUser;

if(defined($ENV{'RESPONDER_PACKET'})) {
    printf "Environment variable RESPONDER_PACKET: '%s'\n", $ENV{'RESPONDER_PACKET'};
    %IPCdata=Responder::LoadJsonFile($ENV{'RESPONDER_PACKET'});
    print Dumper \%IPCdata;
} else {
    print "IPC is broken; no responder packet defined.\n";
}
