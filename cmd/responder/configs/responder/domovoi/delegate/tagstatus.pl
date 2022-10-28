#!/usr/bin/env perl
use strict;
use lib '/data/responder/lib/';
use Responder;
use JSON;
use Data::Dumper;

my $dbh=Responder::MakeDbConnect('events');

my $TagSearch;

$TagSearch=$ARGV[1];
$TagSearch=undef if($TagSearch eq 'undefined');

if($TagSearch) {
 print "*Only '$TagSearch' wirelesstags*\n";
} else {
 print "*Wirelesstags status*\n";
}


my $sql='select uuid,lastlive,isalive,raw from tagstatus where enabled=true;';
my $sth=$dbh->prepare($sql);
$sth->execute;
my ($uuid, $lastlive, $isalive, $raw);
$sth->bind_columns(\$uuid, \$lastlive, \$isalive, \$raw);

my %Block;
while($sth->fetch)
 {
  $lastlive=~s/\..*//;
  my $Name=$uuid;
  my $Temp;
  if($raw ne "")
   {
    my $Rec=decode_json($raw);
    $Name=$$Rec{'Name'};
    $Temp=$$Rec{'Temperature'};
   }
  $Temp=sprintf('%.01f', ($Temp*1.8)+32) if($Temp);
  my $Category;
  my $emoji='gray_question';
  my $Comment='';
  if($isalive==1)
   {
    $Category='Alive';
    $emoji='thumbsup';
    if($Temp>82) { $emoji='fire'; }
    if($Temp<70) { $emoji='snowflake'; }
    $Comment=sprintf('%sF; last check-in %s', $Temp, $lastlive);
   } else {
    $Category='Dead';
    $emoji='skull';
    $Comment='last check-in '.$lastlive if($lastlive);
   }
  $Comment=": $Comment" if($Comment ne '');
  push @{$Block{$Category}}, (sprintf ":%s: %s%s\n", $emoji, $Name, $Comment);
 }
$sth->finish;

foreach my $Caw (sort keys %Block)
 {
  next if($TagSearch&&$TagSearch ne lc($Caw));
  printf "*%s*\n", $Caw unless($TagSearch);
  foreach (@{$Block{$Caw}})
   {
    print $_;
   }
 }
