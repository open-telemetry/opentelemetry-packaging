# frozen_string_literal: true

require 'socket'
require 'uri'
require 'net/http'

server = TCPServer.new('0.0.0.0', 3000)

Thread.new do
  sleep 1
  loop do
    begin
      Net::HTTP.get_response(URI('http://127.0.0.1:3000/internal'))
    rescue StandardError => e
      warn "self-request failed: #{e.message}"
    end
    sleep 0.5
  end
end

loop do
  client = server.accept
  begin
    request_line = client.gets
    next if request_line.nil?
    while (line = client.gets)
      break if line == "\r\n"
    end
    body = "ok\n"
    client.write "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: #{body.bytesize}\r\nConnection: close\r\n\r\n#{body}"
  ensure
    client.close
  end
end
