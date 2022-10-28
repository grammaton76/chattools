#!/usr/bin/env perl
use strict;
use warnings FATAL => 'all';
use FindBin;
sub LibPath() { my $Caw=$FindBin::Bin; $Caw=~s/\/baytor\/responder\/.*/\/baytor\/responder\/lib/; return $Caw; }
use lib LibPath();
use Responder;
use JSON;
use Data::Dumper;

my %Config=Responder::LoadConfigIni("/data/baytor/homenet.ini");

my %SearchTags =
    (
        'expired' => 'Expired clients',
        'tagged' => 'Tagged clients',
        'active' => 'Active clients and their IPs',
        'expiration' => 'All active clients and when they expire',
        'untagged' => 'All untagged clients in history'
    );

sub GiveUsage() {
 print "Usage:\n";
 foreach (sort keys %SearchTags) {
  printf " \`%s\` - %s\n", $_, $SearchTags{$_};
 }
 print " \`tag <macaddr> <name>\` - tag a device with that specific nickname.\n";
 exit;
}

sub QueryDhcpClients($) {
 my ($Arg)=@_;
 $Arg=lc($Arg);
 my $Where;
 my $ShowExpiration;
 my $ShowIP;
 if($Arg eq 'active') {
  $Where = 'where expired=false';
  $ShowIP=1;
 } elsif($Arg eq 'expiration') {
  $Where='where expired=false';
  $ShowExpiration=1;
 } elsif($Arg eq 'expired') {
  $Where='where expired=true';
 } elsif($Arg eq 'untagged') {
  $Where='where nickname is null';
 } else {
  GiveUsage();
 }
 my $dbh=Responder::ConnectToDbViaKey('dhcptracker.dbsection');
 my $sql="select mac,nickname,lastip,firstseen,lastseen,clienthostname,expiration from dhcphosts $Where ORDER BY nickname,mac;";
 my $sth=$dbh->prepare($sql);
 $sth->execute;
 my ($mac,$nickname,$lastip,$firstseen,$lastseen,$clienthostname,$expiration);
 $sth->bind_columns(\$mac,\$nickname,\$lastip,\$firstseen,\$lastseen,\$clienthostname,\$expiration);
 while($sth->fetch)
 {
  $firstseen=~s/\..*//;
  $lastseen=~s/\..*//;
  my $ShowName='noname';
  $ShowName=$clienthostname if($clienthostname);
  $ShowName="$nickname" if($nickname);
  $ShowName=sprintf("%s '%s'", $mac, $ShowName);
  my $emoji='grey_question';
  my $Comment='';
  if($nickname) {
   $emoji='white_check_mark';
  }
  unless($ShowExpiration)
  {
   $Comment=sprintf('@ %s; last seen %s', $lastip, $lastseen);
  } else {
   $Comment=sprintf("expires at '%s'; last check-in '%s'", $expiration, $lastseen);
  }
  $Comment=": $Comment" if($Comment ne '');
  printf ":%s: %s%s\n", $emoji, $ShowName, $Comment;
 }
 $sth->finish;
}

sub TagDevice() {
 my (undef, $Mac, $Nickname)=@ARGV;
 #print "Command: $Command\nMac: $Mac\nNickname: $Nickname\n"; exit;
 unless(lc($Mac)=~m/^[\da-f\:]+$/)
 {
  print "MAC address '$Mac' is not valid; must be 6 colon-delimited hex pairs.\n";
  exit;
 }
 unless($Nickname) {
  print "No nickname specified. Usage is tag [macaddr] 'nickname'\n";
  exit;
 }
 print "Tagging...\n";
 my $dbh=Responder::ConnectToDbViaKey('dhcptracker.dbsection');
 my $sql='UPDATE dhcphosts SET nickname=? WHERE mac=?;';
 my $sth=$dbh->prepare($sql);
 $sth->execute($Nickname, $Mac);
 $sth->finish;
 printf "Set mac '%s' to have nickname '%s'\n", $Mac, $Nickname;
}

my $ParamCount=@ARGV;

if ($ParamCount==0) {
 GiveUsage();
}

if ($SearchTags{$ARGV[0]}) {
 printf "*%s:*\n", $SearchTags{$ARGV[0]};
 QueryDhcpClients($ARGV[0]);
} elsif ($ARGV[0] eq 'tag') {
 TagDevice();
} else {
 GiveUsage();
}