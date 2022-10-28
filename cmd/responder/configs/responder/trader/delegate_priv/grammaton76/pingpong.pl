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

sub Exit($) {
 my ($Msg)=@_;
 print $Msg;
 exit;
}

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

if($Command=~m/^\d+$/) {
 print "Adjust order number: not implemented.\n";
} elsif(uc($Command)=~m/^[A-Z\d]+\-[A-Z\d]+$/) {
 $Command=uc($Command);
 print "Market $Command detected.\n";
 my $dbh=Responder::ConnectToDb(\%Config, 'strader');
 my $sql='select id,market,buylimit,selllimit,buyqty,sellqty,reinvest_ratio,restock_ratio from strat_pingpong WHERE market=? ORDER BY buylimit,selllimit;';
 my $sth=$dbh->prepare($sql);
 $sth->execute($Command);
 my %Block;
printf "All pingpongs for market '%s'. DON'T FORGET TO CODE CHECKS FOR ACTIVE ORDERS.\n", $Command;
 while(my $Rec=$sth->fetchrow_hashref)
 {
  my $Market=$$Rec{'market'};
  my $Id=$$Rec{'id'};
  my ($BuyLimit,$SellLimit,$BuyQty,$SellQty)=Responder::s8f($$Rec{'buylimit'}, $$Rec{'selllimit'}, $$Rec{'buyqty'}, $$Rec{'sellqty'});
  my ($Reinvest, $Restock)=Responder::s3f($$Rec{'reinvest_ratio'},$$Rec{'restock_ratio'});
  printf "%s.%s: %s @ %s - %s @ %s; reinvest %s; restock %s\n", $Market, $Id, $BuyQty, $BuyLimit, $SellQty, $SellLimit, $Reinvest, $Restock;
 }
 $sth->finish;
} elsif($Command eq 'set') {
 my @ARGS=@ARGV;
 shift @ARGS; # This is the command; already read.
 my $PongId=uc(shift @ARGS) or Exit("No pingpong id specified (i.e. BTC-DOGE.410)\n");
 my $SkipNext;
 my $Prev=undef;
 my %Packet;
 my %ParseRules = (
   'keyword' => {
     'buylimit' => 'F',
     'selllimit' => 'F',
     'buyqty' => 'F',
     'sellqty' => 'F',
     'mode' => 'S',
     'ratio' => 'F',
     'reinvest' => 'F',
     'restock' => 'F',
     'state' => 'S',
    },
    'values' => {
     'mode' => [ 'BUY', 'SELL' ],
     'state' => [ 'INIT', 'RUNNING', 'RETRUE', 'SWITCH', 'RESUME', 'STOP', 'STOPNEXT' ]
    }
  );
 #print Dumper \%ParseRules;
 #print Dumper \@ARGS;
 #print "\n";
 foreach my $Current (@ARGS)
  {
   $Current=lc($Current);
   #print "Attempting to parse '$Current'\n";
   if(!defined($Prev) && defined($ParseRules{$Current}) && $ParseRules{$Current} eq '') {
    $Packet{$Current}='';
    print "Iterating to the next word; present is '%s'\n", $Current;
    next;
   }
   my $Rule=undef;
   if(defined($Prev)) {
     if(defined($ParseRules{'keyword'}{$Prev})) {
       $Rule=$ParseRules{'keyword'}{$Prev};
      }
    } else {
      if(defined($ParseRules{'keyword'}{$Current})) {
        $Prev=$Current;
        #printf "Detected incoming command '%s'\n", $Current;
        next;
      }
    }
   if(!defined($Rule)) {
    Exit("FAIL: unknown parameter '${Current}' passed.\n");
   } elsif($Rule eq 'F') {
    if($Current=~m/^\d*\.?\d+$/) {
     $Packet{$Prev}=sprintf "%.08f", $Current;
     $Prev=undef;
    } else {
     Exit("$Prev parameter must be a number (decimal is ok); you provided '${Current}'\n");
    }
   } elsif($Rule eq 'S') {
    my $Found;
    foreach (@{$ParseRules{'values'}{$Prev}}) {
      if(lc($_) eq lc($Current))
       {
        $Found=1;
        $Packet{$Prev}=$_;
       }
     }
    Exit("Inappropriate value '${Current}' supplied to keyword '${Prev}'; legal values are '@{$ParseRules{'values'}{$Prev}}'\n") unless($Found);
    $Prev=undef;
   }
  }
 if(defined($Prev)) {
  Exit("Failed to specify a value for final set '$Prev'.\n");
  }
 unless($PongId=~m/(\w+\-\w+)\.(\d+)/) {
   Exit("Pingpong id specifier '${PongId}' invalid; valid identifiers are of the form BTC-USD.32.\n");
  }
 my $dbh=Responder::ConnectToDb(\%Config, 'strader');
 my ($Market, $Id)=($1, $2);
 my $sql='select buylimit,selllimit,buyqty,sellqty,mode,suspended,state from strat_pingpong WHERE market=? AND id=? ORDER BY buylimit,selllimit;';
 my $sth=$dbh->prepare($sql);
 $sth->execute($Market, $Id);
 my %Block;
 printf "Pre-change data on '%s'.\n", $PongId;
 while(my $Rec=$sth->fetchrow_hashref)
  {
   my ($BuyLimit,$SellLimit,$BuyQty,$SellQty)=Responder::s8f($$Rec{'buylimit'}, $$Rec{'selllimit'}, $$Rec{'buyqty'}, $$Rec{'sellqty'});
   my ($Reinvest, $Restock)=Responder::s3f($$Rec{'reinvest_ratio'},$$Rec{'restock_ratio'});
   my ($Mode, $State)=Responder::s($$Rec{'mode'}, $$Rec{'state'});
   my ($Suspended)=Responder::b($$Rec{'suspended'});
   printf "%s buylimit %s buyqty %s selllimit %s sellqty %s reinvest %s restock %s mode %s suspended %s state %s\n",
    $PongId, $BuyLimit, $BuyQty, $SellLimit, $SellQty, $Reinvest, $Restock, $Mode, $Suspended, $State;
  }
$sth->finish; 

 my $SqlSet;
 {
  my %SqlField;
  $SqlField{'reinvest'}='reinvest_ratio';
  $SqlField{'restock'}='restock_ratio';
  my @Sets;
  foreach (sort keys %Packet) {
    my $SetField=$_;
    $SetField=$SqlField{$SetField} if(defined($SqlField{$SetField}));
    push @Sets, sprintf "%s=%s", $SetField, $dbh->quote($Packet{$_});
   }
  $SqlSet=join(", ", @Sets);
 }
 $sql=sprintf 'UPDATE strat_pingpong SET %s WHERE id=%d AND market=%s;', $SqlSet, $Id, $dbh->quote($Market);
 $dbh->do($sql);
 if($dbh->errstr) {
   printf "Result '%s' from sql '%s'\n", $dbh->errstr, $sql;
  } else {
   printf "SQL executed: %s\n", $sql;
  }
} elsif($Command=~m/(\w+\-\w+)\.(\d+)/) {
 my ($Market, $Id)=(uc($1), $2);
 my $dbh=Responder::ConnectToDb(\%Config, 'strader');
 my $sql='select buylimit,selllimit,buyqty,sellqty,mode,suspended,state from strat_pingpong WHERE market=? AND id=? ORDER BY buylimit,selllimit;';
 my $sth=$dbh->prepare($sql);
 $sth->execute($Market, $Id);
 my %Block;
 printf "Present settings for '%s':\n", $Command;
 while(my $Rec=$sth->fetchrow_hashref)
  {
   my ($BuyLimit,$SellLimit,$BuyQty,$SellQty)=Responder::s8f($$Rec{'buylimit'}, $$Rec{'selllimit'}, $$Rec{'buyqty'}, $$Rec{'sellqty'});
   my ($Reinvest, $Restock)=Responder::s3f($$Rec{'reinvest_ratio'},$$Rec{'restock_ratio'});
   my ($Mode, $State)=Responder::s($$Rec{'mode'}, $$Rec{'state'});
   my ($Suspended)=Responder::b($$Rec{'suspended'});
   printf "\`%s buylimit %s buyqty %s selllimit %s sellqty %s reinvest %s restock %s mode %s suspended %s state %s\`\n",
    $Command, $BuyLimit, $BuyQty, $SellLimit, $SellQty, $Reinvest, $Restock, $Mode, $Suspended, $State;
  }
$sth->finish; 
$dbh->disconnect;
} else {
 print "No idea what '$Command' means, nor any of the rest of the stuff you typed.\n";
}
