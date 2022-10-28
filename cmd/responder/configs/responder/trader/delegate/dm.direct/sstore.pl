#!/usr/bin/env perl
use strict;
use warnings FATAL => 'all';
use FindBin;
sub LibPath() { my $Caw=$FindBin::Bin; $Caw=~s/\/baytor\/responder\/.*/\/baytor\/responder\/lib/; return $Caw; }
use lib LibPath();
use Responder;
use JSON;
use Data::Dumper;

#pricehistory ILCE*
#pricehistory ILCEMK4 (default prefix, minimum two characters);

my %Config=Responder::LoadAnIni("/data/baytor/storescrape.ini");
my %IPCdata;

my $ARGC=@ARGV;
my $RemoteUser;
my $RemoteUserId;
my $dbh;

if(defined($ENV{'RESPONDER_PACKET'})) {
#    printf "Environment variable RESPONDER_PACKET: '%s'\n", $ENV{'RESPONDER_PACKET'};
    %IPCdata=Responder::LoadJsonFile($ENV{'RESPONDER_PACKET'});
#    print Dumper \%IPCdata; exit;
    unless(defined($IPCdata{'sender'})) {
        print "IPC packet is missing sender; system broken.\n";
        exit;
    }
    $RemoteUser=$IPCdata{'sender'};
    unless(defined($IPCdata{'senderid'})) {
        print "IPC packet is missing senderid; system broken.\n";
        exit;
    }
    $RemoteUserId=$IPCdata{'senderid'};
} else {
    print "IPC is broken; no responder packet defined.\n";
    exit;
}

{
    my $Authorized;
    $Authorized=1 if(-f $Config{'chat.userauthdir'}."/$RemoteUser");
    $Authorized=1 if(-f $Config{'chat.userauthdir'}."/$RemoteUserId");
    unless($Authorized) {
        print "*You haven't been authorized. Contact snewton to request access; tell him your username is '$RemoteUser' and RemoteUserId '$RemoteUserId'.*\n";
        exit;
    }
}

sub UsageAndExit() {
    print "Available commands:\n";
    print " * \`sstore search ILCE* [all]\` - Current status of all items in the store with id's starting with 'ILCE'; add the word 'all' after your search term if you want to also see out-of-stock.\n";
    print "COMING EVENTUALLY:\n";
    print " * \`sstore pricehistory ILCE*\` - Price history for all items beginning with 'ILCE'\n";
    print " * \`sstore pricehistory ILCE7R/B\` - Search specifically for the ILCE7R/B.\n";
    exit;
}

if($ARGC eq 0) {
    UsageAndExit();
}

sub ProductIdToWhere($) {
    my $Input=lc($ARGV[1]);
    my $Search;
    unless($Input=~m/^[\da-z\-\/\s]+\*?$/)
    {
        print "Product id '$Input' is not valid; contains illegal characters.\n";
        exit;
    }
    if($Input=~s/\*$/%/) {
        $Search="WHERE lower(productid) LIKE ".$dbh->quote($Input);
    } else {
        $Search="WHERE lower(productid)=".$dbh->quote($Input);
    }
    return $Search;
}

sub SearchCurrentItems($$) {
    my ($sql,$Settings)=@_;
    my $Buffer;
    my $All;
    $All=1 if(defined($$Settings{'all'}));
    my $sth=$dbh->prepare($sql);
    $sth->execute;
    my $Suppressed;
    my ($productid,$link,$price,$strikeprice,$firstseen,$lastseen,$message);
    $sth->bind_columns(\$productid,\$link,\$price,\$strikeprice,\$firstseen,\$lastseen,\$message);
    while($sth->fetch)
    {
        my $StockEmoji=':large_green_circle:';
        if(lc($message)=~m/out of stock/) {
            unless($All)
            {
                ++$Suppressed;
                next;
            }
            $StockEmoji=':red_circle:';
        }
        unless($All) {
            $StockEmoji='';
        }
        if($message) {
            $message=" - $message";
        }
        $Buffer.=sprintf "%s<%s|%s> ~\$%s~ \$%s%s\n", $StockEmoji, $link, $productid, $strikeprice, $price, $message;
    }
    $sth->finish;
    if($Suppressed) {
        $Buffer.=sprintf "_%d items suppressed due to stock status; add 'all' after your search term to see out-of-stock too._\n", $Suppressed;
    }
    return $Buffer;
}

my $Command=lc($ARGV[0]);
if($Command eq 'search') {
    $dbh=Responder::ConnectToDb(\%Config, 'scraperdb');
    my ($OrigSearch, $All);
    my $Search='';
    if($ARGC>1) {
        $OrigSearch=$ARGV[1];
        $Search=ProductIdToWhere($OrigSearch);
        if($ARGC>2) {
            if(lc($ARGV[2]) eq 'all') {
                $All=1;
            }
        }
    } else {
        print "You must provide something to search by.\n";
        exit;
    }
    printf "Search for items matching: \`%s\`\n", $OrigSearch;
    my $sql="select productid,link,price,strikeprice,firstseen,lastseen,message from item_current $Search order by productid;";
    my $Buf=SearchCurrentItems($sql, { 'all' => $All});
    if($Buf)
     {
      print $Buf;
     } else {
      print "No current matches for '$OrigSearch'. If you wanted a wildcard search, perhaps you forgot a * at the end?\n";
     }
    exit;
} elsif($Command eq 'pricehistory') {
    my $OrigSearch=$ARGV[1];
    $dbh=Responder::ConnectToDb(\%Config, 'scraperdb');
    my $Search=ProductIdToWhere($OrigSearch);
    printf "Change history for items matching: \`%s\`\n", $OrigSearch;
    my $sql="select productid,detected,oldrec->>'Price',oldrec->>'Link',oldrec->>'Message' from item_history $Search order by productid,detected;";
    my $sth=$dbh->prepare($sql);
    $sth->execute;
    my ($productid,$detected,$link,$price,$message);
    $sth->bind_columns(\$productid,\$detected,\$price,\$link,\$message);
    while($sth->fetch)
    {
        my $StockEmoji='large_green_circle';
        if(lc($message)=~m/out of stock/) {
            $StockEmoji='red_circle';
        }
        $detected=~s/:\d\d\..*//;
        if($message) {
            $message=" - $message";
        }
        printf ":%s:<%s|%s> \$%s on '%s'%s\n", $StockEmoji, $link, $productid, $price, $detected, $message;
    }
    $sth->finish;
    print "\n*Current status:*\n";
    $sql="select productid,link,price,strikeprice,firstseen,lastseen,message from item_current $Search order by productid;";
    my $Buf=SearchCurrentItems($sql, { 'all' => 1 });
    if($Buf)
     {
      print $Buf;
     } else {
      print "No current matches for '$OrigSearch'. If you wanted a wildcard search, perhaps you forgot a * at the end?\n";
     }
    exit;
} else {
    UsageAndExit();
    exit;
}
