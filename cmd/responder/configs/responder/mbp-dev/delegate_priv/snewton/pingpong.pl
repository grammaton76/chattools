#!/usr/bin/env perl
use strict;
use warnings FATAL => 'all';
use FindBin;
sub LibPath() { my $Caw=$FindBin::Bin; $Caw=~s/\/baytor\/responder\/.*/\/baytor\/responder\/lib/; return $Caw; }
use lib LibPath();
use Responder;
use JSON;
use Data::Dumper;

my %Config=Responder::LoadAnIni("/data/baytor/tradechat.ini");

my $ARGC=@ARGV;

if($ARGC eq 0) {
    my $dbh=Responder::ConnectToDb(\%Config, 'strader');
    my $sql='select market,count(*) from strat_pingpong group by market;';
    my $sth=$dbh->prepare($sql);
    $sth->execute;
    my ($market,$count);
    $sth->bind_columns(\$market,\$count);
    my %Block;
    while($sth->fetch)
    {
        printf "%s: %s pingpongs\n", $market, $count;
    }
    $sth->finish;
    exit;
}

my $Command=$ARGV[0];
print "Command not specified.\n";
